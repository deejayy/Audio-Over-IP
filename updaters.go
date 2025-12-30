package main

import (
	wRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Per-server status update channel; carries server ID and status text as JSON
var connStatus = make(chan ConnStatusMessage, 100)

type ConnStatusMessage struct {
	ServerID   string `json:"serverId"`
	StatusText string `json:"statusText"`
}

type ConnStatuses struct {
	Idle                       string
	Connected                  string
	ConnectionFailed           string
	Connecting                 string
	ConnectionLost             string
	InvalidIpOrPort            string
	ResamplingFailed           string
	AudioConfigRetrievalFailed string
	CaptureDeviceUnavailable   string
}

var connStatuses = ConnStatuses{
	Idle:                       "Idle",
	Connected:                  "Connected",
	ConnectionFailed:           "Connection failed",
	Connecting:                 "Connecting...",
	ConnectionLost:             "Connection lost",
	InvalidIpOrPort:            "Invalid IP or Port",
	ResamplingFailed:           "Resampling failed",
	AudioConfigRetrievalFailed: "Could not retrieve server audio config",
	CaptureDeviceUnavailable:   "Capture Device Unavailable",
}

func connStatusUpdater() {
	for msg := range connStatus {
		// Update server registry status under lock so snapshots reflect current state
		serversMu.Lock()
		if s, ok := servers[msg.ServerID]; ok {
			s.Status = msg.StatusText
		}
		serversMu.Unlock()

		// Emit the per-server status update
		wRuntime.EventsEmit(app.ctx, "updateConnStatus", msg)

		wRuntime.EventsEmit(app.ctx, "serversUpdated", ListServers())
	}
}

// ServerStats holds statistics about the server.
type ServerStats struct {
	ConnectedClients int `json:"connectedClients"`
	Bandwidth        int `json:"bandwidth"` // in kbps
}

// serverStats is a channel for ServerStats updates.
var serverStats = make(chan ServerStats, 10)

// serverStatsUpdater listens for server stats updates and emits them to the frontend.
func serverStatsUpdater() {
	for stats := range serverStats {
		wRuntime.EventsEmit(app.ctx, "updateServerStats", stats)
	}
}
