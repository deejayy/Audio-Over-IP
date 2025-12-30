package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"AuOvIP/wcatools"

	"github.com/gorilla/websocket"
)

// Data transmitted over /info endpoint
type InfoData struct {
	Hostname     string                 `json:"hostname"`
	AudioDevices []wcatools.AudioConfig `json:"audioDevices"`
}

// serverCtl holds runtime state for the running server so we can start/stop
// it cleanly using a context cancellation and waitgroup.
var serverCtl struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running bool
}

// websocket clients (single port for HTTP + audio streaming)
type wsClient struct {
	conn      *websocket.Conn
	sendCh    chan []byte
	deviceID  string
	bytesSent uint64
}

var wsClients = make(map[int]*wsClient)
var wsClientsMu sync.Mutex
var infoClients = make(map[int]*websocket.Conn)
var infoClientsMu sync.Mutex
var nextWSID = 0

// CaptureSession manages a single audio device capture loop
type CaptureSession struct {
	deviceID string
	dr       *wcatools.DeviceReader
	clients  map[int]*wsClient
	mu       sync.Mutex
	stopCh   chan struct{}
	running  bool
}

type CaptureManager struct {
	mu       sync.Mutex
	sessions map[string]*CaptureSession
	ctx      context.Context
}

var captureManager *CaptureManager

func startServer(listenPort string) {
	// Capture loop runs on this goroutine, using COM (STA).
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	serverCtl.mu.Lock()
	if serverCtl.running {
		serverCtl.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	serverCtl.cancel = cancel
	serverCtl.running = true
	serverCtl.mu.Unlock()
	defer func() {
		select {
		case <-ctx.Done():
			return
		default:
			cancel()
		}
	}()

	// Initialize COM
	if err := wcatools.InitCOM(); err != nil {
		serverLogger.Errorf("InitCOM failed: %s", err.Error())
		return
	}
	defer wcatools.UninitCOM()

	// Initialize CaptureManager
	captureManager = &CaptureManager{
		sessions: make(map[string]*CaptureSession),
		ctx:      ctx,
	}

	// Start Device Monitor
	stopMonitor := wcatools.StartMonitor(func(event wcatools.DeviceEvent, deviceID string) {
		// Broadcast new info to all info clients
		broadcastInfo()

		// Handle device removal
		if event == wcatools.DeviceRemoved {
			captureManager.mu.Lock()
			if session, ok := captureManager.sessions[deviceID]; ok {
				// Stop session
				close(session.stopCh)
				delete(captureManager.sessions, deviceID)
			}
			captureManager.mu.Unlock()
		}
	})
	defer stopMonitor()

	// start control HTTP server
	serverCtl.wg.Add(1)
	go func() {
		defer serverCtl.wg.Done()
		startServing(ctx, listenPort)
	}()

	// Start stats updater
	serverCtl.wg.Add(1)
	go func() {
		defer serverCtl.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				// Send final zeroed stats
				serverStats <- ServerStats{
					ConnectedClients: 0,
					Bandwidth:        0,
				}
				return
			case <-ticker.C:
				var totalBytesSent uint64
				wsClientsMu.Lock()
				for _, client := range wsClients {
					curVal := atomic.LoadUint64(&client.bytesSent)
					atomic.AddUint64(&client.bytesSent, ^uint64(curVal-1))
					totalBytesSent += curVal
				}
				clientCount := len(wsClients)
				wsClientsMu.Unlock()

				// Calculate bandwidth in kbps
				bandwidth := (totalBytesSent) * 8 / 1000 //Using 1000 for KILObits (KB) and not 1024 for KIBIbits (KiB)

				stats := ServerStats{
					ConnectedClients: clientCount,
					Bandwidth:        int(bandwidth),
				}
				serverStats <- stats
			}
		}
	}()

	// Wait for context done
	<-ctx.Done()

	// Cleanup
	captureManager.mu.Lock()
	for id, session := range captureManager.sessions {
		close(session.stopCh)
		delete(captureManager.sessions, id)
	}
	captureManager.mu.Unlock()

	wsClientsMu.Lock()
	for _, cl := range wsClients {
		close(cl.sendCh)
		cl.conn.Close()
	}
	wsClientsMu.Unlock()

	infoClientsMu.Lock()
	for _, conn := range infoClients {
		conn.Close()
	}
	infoClientsMu.Unlock()
}

func stopServer() {
	serverCtl.mu.Lock()
	if !serverCtl.running {
		serverCtl.mu.Unlock()
		return
	}
	cancel := serverCtl.cancel
	serverCtl.mu.Unlock()

	// signal cancellation and wait for goroutines to finish
	cancel()
	serverCtl.wg.Wait()

	serverCtl.mu.Lock()
	serverCtl.running = false
	serverCtl.cancel = nil
	serverCtl.mu.Unlock()
}

func broadcastInfo() {
	hostname, _ := os.Hostname()
	devices, _ := wcatools.ListDevices()
	info := InfoData{
		Hostname:     hostname,
		AudioDevices: devices,
	}

	infoClientsMu.Lock()
	defer infoClientsMu.Unlock()
	for _, conn := range infoClients {
		conn.WriteJSON(info)
	}
}

func startServing(ctx context.Context, listenPort string) {
	doneServing := false
	defer func() {
		doneServing = true
	}()
	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:    ":" + listenPort,
		Handler: mux,
	}

	mux.HandleFunc("/getAudioDevices", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case "GET":
			devices, err := wcatools.ListDevices()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			devicesBytes, err := json.MarshalIndent(devices, "", "    ")
			if err != nil {
				return
			}
			w.Write(devicesBytes)
		}
	})

	mux.HandleFunc("/getAudioConfig", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case "GET":
			query := req.URL.Query()
			deviceID := query.Get("deviceID")

			var err error
			var aconf wcatools.AudioConfig
			for i := 0; i < 5; i++ {
				if deviceID == "" {
					http.Error(w, "deviceID missing in request", http.StatusBadRequest)
					return
				}
				if deviceID == "default" {
					aconf, err = wcatools.GetDefaultDeviceInfo()
					if err == nil {
						break
					}
				} else {
					aconf, err = wcatools.GetDeviceByID(deviceID)
					if err == nil {
						break
					}
				}
				time.Sleep(time.Millisecond * 50)
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			aconfBytes, err := json.MarshalIndent(aconf, "", "    ")
			if err != nil {
				return
			}
			w.Write(aconfBytes)
		}
	})

	// Info endpoint for initial connection and heartbeat
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverLogger.Errorf("info websocket upgrade failed: %v", err)
			return
		}

		wsClientsMu.Lock()
		id := nextWSID
		nextWSID++
		wsClientsMu.Unlock()

		infoClientsMu.Lock()
		infoClients[id] = conn
		infoClientsMu.Unlock()

		defer func() {
			infoClientsMu.Lock()
			delete(infoClients, id)
			infoClientsMu.Unlock()
			conn.Close()
		}()

		hostname, _ := os.Hostname()
		devices, _ := wcatools.ListDevices()
		info := InfoData{
			Hostname:     hostname,
			AudioDevices: devices,
		}
		if err := conn.WriteJSON(info); err != nil {
			return
		}

		// Keep connection alive for heartbeat
		for {
			if _, _, err := conn.NextReader(); err != nil {
				break
			}
		}
	})

	mux.HandleFunc("/audio", func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverLogger.Errorf("websocket upgrade failed: %v", err)
			return
		}
		serverLogger.Infof("new websocket upgrade from %s", r.RemoteAddr)

		// Get requested device ID
		requestedDeviceID := r.URL.Query().Get("deviceID")
		if requestedDeviceID == "" || requestedDeviceID == "default" {
			// Resolve default
			defID, err := wcatools.GetDefaultDeviceID()
			if err == nil {
				requestedDeviceID = defID
			} else {
				serverLogger.Errorf("failed to resolve default device: %v", err)
				conn.Close()
				return
			}
		}

		// register client
		wsClientsMu.Lock()
		id := nextWSID
		nextWSID++
		cl := &wsClient{
			conn:     conn,
			sendCh:   make(chan []byte, 80),
			deviceID: requestedDeviceID,
		}
		wsClients[id] = cl
		wsClientsMu.Unlock()
		serverLogger.Infof("registered ws client id=%d remote=%s device=%s", id, r.RemoteAddr, requestedDeviceID)

		// Add to CaptureManager
		captureManager.mu.Lock()
		session, ok := captureManager.sessions[requestedDeviceID]
		if !ok {
			// Create new session
			dr, err := wcatools.NewDeviceReader(requestedDeviceID)
			if err != nil {
				serverLogger.Errorf("failed to create device reader for %s: %v", requestedDeviceID, err)
				captureManager.mu.Unlock()
				conn.Close()
				return
			}
			session = &CaptureSession{
				deviceID: requestedDeviceID,
				dr:       dr,
				clients:  make(map[int]*wsClient),
				stopCh:   make(chan struct{}),
				running:  true,
			}
			captureManager.sessions[requestedDeviceID] = session

			// Start capture loop
			go func(s *CaptureSession) {
				defer s.dr.Close()
				for {
					select {
					case <-s.stopCh:
						return
					default:
						audioData, err := s.dr.Read()
						if err != nil {
							serverLogger.Errorf("read error for %s: %v", s.deviceID, err)
							return
						}
						if len(audioData) == 0 {
							time.Sleep(time.Millisecond)
							continue
						}

						s.mu.Lock()
						for _, c := range s.clients {
							select {
							case c.sendCh <- audioData:
							default:
							}
						}
						s.mu.Unlock()
					}
				}
			}(session)
		}
		session.mu.Lock()
		session.clients[id] = cl
		session.mu.Unlock()
		captureManager.mu.Unlock()

		const (
			pingPeriod = 2 * time.Second
			pongWait   = 5 * time.Second
		)

		// reader goroutine to handle Pongs and detect dead connections
		go func(id int, cl *wsClient) {
			defer func() {
				// Remove from global list
				wsClientsMu.Lock()
				if _, ok := wsClients[id]; ok {
					delete(wsClients, id)
					cl.conn.Close()
				}
				wsClientsMu.Unlock()

				// Remove from session
				captureManager.mu.Lock()
				if session, ok := captureManager.sessions[cl.deviceID]; ok {
					session.mu.Lock()
					delete(session.clients, id)
					clientCount := len(session.clients)
					session.mu.Unlock()

					if clientCount == 0 {
						// Stop session if no clients left
						close(session.stopCh)
						delete(captureManager.sessions, cl.deviceID)
					}
				}
				captureManager.mu.Unlock()
			}()

			cl.conn.SetReadLimit(512)
			cl.conn.SetReadDeadline(time.Now().Add(pongWait))
			cl.conn.SetPongHandler(func(string) error {
				cl.conn.SetReadDeadline(time.Now().Add(pongWait))
				return nil
			})

			for {
				_, _, err := cl.conn.ReadMessage()
				if err != nil {
					break
				}
			}
		}(id, cl)

		// writer goroutine
		go func(id int, cl *wsClient) {
			ticker := time.NewTicker(pingPeriod)
			defer func() {
				ticker.Stop()
				// Cleanup handled by reader
			}()

			for {
				select {
				case data, ok := <-cl.sendCh:
					if !ok {
						cl.conn.WriteMessage(websocket.CloseMessage, []byte{})
						return
					}
					cl.conn.SetWriteDeadline(time.Now().Add(pongWait))
					if err := cl.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
						return
					}
					atomic.AddUint64(&cl.bytesSent, uint64(len(data)))

				case <-ticker.C:
					cl.conn.SetWriteDeadline(time.Now().Add(pongWait))
					if err := cl.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
						return
					}
				}
			}
		}(id, cl)

	})
	// listen for context cancellation to shut down the HTTP server
	go func() {
		<-ctx.Done()
		if !doneServing {
			srv.Close()
		}
	}()
	srv.ListenAndServe()
}
