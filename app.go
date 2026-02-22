package main

import (
	"context"
	_ "embed"
	"os/exec"
	"runtime"
	"sync"

	"AuOvIP/pcmresample"
	"AuOvIP/wcatools"

	"github.com/getlantern/systray"
	wRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed build/windows/icon.ico
var trayIcon []byte

// App struct
type App struct {
	ctx              context.Context
	trayOnce         sync.Once
	quitMutex        sync.Mutex
	allowQuit        bool
	serverToggleItem *systray.MenuItem
	serverStatusChan chan bool
	serverListChan   chan []Server
	serverMenuItems  map[string]*systray.MenuItem
	serverMenuMu     sync.Mutex
}

func (a *App) GetServerConfig() ServerConfig {
	return serverConfig
}

func (a *App) SaveServerConfig(cfg ServerConfig) error {
	configMu.Lock()
	serverConfig = cfg
	configMu.Unlock()
	wRuntime.EventsEmit(a.ctx, "updateServerPagePortDisplay", cfg.Port)
	return SaveServerConfig()
}

func (a *App) GetClientConfig() ClientConfig {
	return clientConfig
}

func (a *App) SaveClientConfig(cfg ClientConfig) error {
	configMu.Lock()
	clientConfig = cfg
	configMu.Unlock()
	return SaveClientConfig()
}

func (a *App) GetLastActiveMode() string {
	configMu.Lock()
	defer configMu.Unlock()
	return appSettings.LastActiveMode
}

func (a *App) SaveLastActiveMode(mode string) error {
	configMu.Lock()
	appSettings.LastActiveMode = mode
	configMu.Unlock()
	return SaveAppSettings()
}

func (a *App) GetAppSettings() AppSettings {
	configMu.Lock()
	defer configMu.Unlock()
	return appSettings
}

func (a *App) SaveAppSettings(settings AppSettings) error {
	configMu.Lock()
	appSettings = settings
	configMu.Unlock()
	return SaveAppSettings()
}

func (a *App) GetConfigDir() string {
	dir, _ := GetConfigDir()
	return dir
}

func (a *App) OpenConfigDir() error {
	dir, err := GetConfigDir()
	if err != nil {
		return err
	}
	return exec.Command("explorer", dir).Start()
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		serverStatusChan: make(chan bool, 10),
		serverListChan:   make(chan []Server, 10),
		serverMenuItems:  make(map[string]*systray.MenuItem),
	}
}

// startup is called at application startup
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.initTray()
	_ = LoadConfig()

	InitDeviceMonitor()

	for _, s := range ListServers() {
		go func(id string) {
			_ = ReconnectServer(id)
		}(s.ID)
	}

	// Auto-start broadcasting if enabled
	configMu.Lock()
	autoStart := appSettings.AutoStartBroadcasting
	configMu.Unlock()

	if autoStart {
		a.EnableServer()
	}
}

// domReady is called after front-end resources have been loaded
func (a App) domReady(ctx context.Context) {
	//Start connection status frontend updater
	go connStatusUpdater()
	go serverStatsUpdater()
}

// beforeClose is called when the application is about to quit,
// either by clicking the window close button or calling runtime.Quit.
// Returning true will cause the application to continue, false will continue shutdown as normal.
func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	if runtime.GOOS != "windows" {
		return false
	}

	if a.shouldAllowQuit() {
		return false
	}

	wRuntime.WindowHide(a.ctx)
	return true
}

// shutdown is called at application termination
func (a *App) shutdown(ctx context.Context) {
	if runtime.GOOS == "windows" {
		systray.Quit()
	}
}

func (a *App) shouldAllowQuit() bool {
	a.quitMutex.Lock()
	defer a.quitMutex.Unlock()
	return a.allowQuit
}

func (a *App) setAllowQuit(allow bool) {
	a.quitMutex.Lock()
	a.allowQuit = allow
	a.quitMutex.Unlock()
}

func (a *App) initTray() {
	if runtime.GOOS != "windows" {
		return
	}

	a.trayOnce.Do(func() {
		go systray.Run(a.onTrayReady, func() {})
	})
}

type serverMenuData struct {
	server         Server
	menuItem       *systray.MenuItem
	refreshItem    *systray.MenuItem
	connectItem    *systray.MenuItem
	disconnectItem *systray.MenuItem
	reconnectItem  *systray.MenuItem
}

func (a *App) onTrayReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("Audio Over IP")
	systray.SetTooltip("Audio Over IP")

	// Create "Inbound Connections" section at the top
	inboundSection := systray.AddMenuItem("Inbound Connections", "List of connected servers")

	// Track server menu items - servers will be added as submenus under Inbound Connections
	serverMenus := make(map[string]*serverMenuData)

	// Initial server list population
	go func() {
		servers := a.GetServerList()
		if len(servers) > 0 {
			select {
			case a.serverListChan <- servers:
			default:
			}
		}
	}()

	systray.AddSeparator()
	showItem := systray.AddMenuItem("Show", "Show Audio Over IP")
	hideItem := systray.AddMenuItem("Hide", "Hide Audio Over IP")
	systray.AddSeparator()

	// Add server toggle menu item
	serverLabel := "Enable Server Mode"
	if serverIsOn {
		serverLabel = "Disable Server Mode"
	}
	a.serverToggleItem = systray.AddMenuItem(serverLabel, "Toggle server mode on/off")

	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit", "Quit Audio Over IP")

	for {
		select {
		case <-showItem.ClickedCh:
			wRuntime.WindowShow(a.ctx)
			wRuntime.WindowUnminimise(a.ctx)
		case <-hideItem.ClickedCh:
			wRuntime.WindowHide(a.ctx)
		case <-a.serverToggleItem.ClickedCh:
			// Toggle server mode
			if serverIsOn {
				a.DisableServer()
			} else {
				a.EnableServer()
			}
		case enabled := <-a.serverStatusChan:
			// Update menu item label when server status changes
			if enabled {
				a.serverToggleItem.SetTitle("Disable Server Mode")
			} else {
				a.serverToggleItem.SetTitle("Enable Server Mode")
			}
		case servers := <-a.serverListChan:
			// Update server menus
			a.updateServerMenus(inboundSection, servers, serverMenus)
		case <-quitItem.ClickedCh:
			a.setAllowQuit(true)
			systray.Quit()
			wRuntime.Quit(a.ctx)
		}
	}
}

func (a *App) updateServerMenus(inboundSection *systray.MenuItem, servers []Server, serverMenus map[string]*serverMenuData) {
	// Remove servers that no longer exist
	for id, menu := range serverMenus {
		found := false
		for _, s := range servers {
			if s.ID == id {
				found = true
				break
			}
		}
		if !found {
			menu.menuItem.Hide()
			delete(serverMenus, id)
		}
	}

	// Add or update existing servers
	for _, server := range servers {
		if menu, exists := serverMenus[server.ID]; exists {
			// Update existing menu item title
			title := server.Hostname
			if title == "" {
				title = server.Addr
			}
			if server.Online {
				title += " ✓"
			} else {
				title += " ✗"
			}
			menu.menuItem.SetTitle(title)
			menu.server = server

			// Disable refresh if connected or connecting
			isConnected := server.Status == "Connected" || server.Status == "Connecting..."
			if isConnected {
				menu.refreshItem.Disable()
			} else {
				menu.refreshItem.Enable()
			}
		} else {
			// Create new menu item as submenu under Inbound Connections
			title := server.Hostname
			if title == "" {
				title = server.Addr
			}
			if server.Online {
				title += " ✓"
			} else {
				title += " ✗"
			}

			menuItem := inboundSection.AddSubMenuItem(title, "Inbound connection: "+server.Addr)
			refreshItem := menuItem.AddSubMenuItem("Refresh", "Refresh connection info")
			connectItem := menuItem.AddSubMenuItem("Connect", "Connect to this server")
			disconnectItem := menuItem.AddSubMenuItem("Disconnect", "Disconnect from this server")
			reconnectItem := menuItem.AddSubMenuItem("Reconnect", "Reconnect to this server")

			menu := &serverMenuData{
				server:         server,
				menuItem:       menuItem,
				refreshItem:    refreshItem,
				connectItem:    connectItem,
				disconnectItem: disconnectItem,
				reconnectItem:  reconnectItem,
			}
			serverMenus[server.ID] = menu

			// Disable refresh if connected or connecting
			isConnected := server.Status == "Connected" || server.Status == "Connecting..."
			if isConnected {
				menu.refreshItem.Disable()
			}

			// Start goroutine to handle clicks for this server
			go a.handleServerMenuClicks(menu)
		}
	}
}

func (a *App) handleServerMenuClicks(menu *serverMenuData) {
	for {
		select {
		case <-menu.refreshItem.ClickedCh:
			go a.CheckServerConnection(menu.server.ID)
		case <-menu.connectItem.ClickedCh:
			go a.ConnectToAudioServer(menu.server.ID)
		case <-menu.disconnectItem.ClickedCh:
			go a.DisconnetFromAudioServer(menu.server.ID)
		case <-menu.reconnectItem.ClickedCh:
			go func() {
				a.DisconnetFromAudioServer(menu.server.ID)
				a.ConnectToAudioServer(menu.server.ID)
			}()
		}
	}
}

func (a *App) notifyTrayServerListChanged(servers []Server) {
	select {
	case a.serverListChan <- servers:
	default:
	}
}

func (a *App) QuitApp() {
	wRuntime.Quit(a.ctx)
}

func (a *App) MinimiseApp() {
	wRuntime.WindowMinimise(a.ctx)
}

// Server management exposed to frontend
func (a *App) AddServer(ipAddress string, port string) error {
	_, err := AddServer(ipAddress, port)
	if err == nil {
		servers := ListServers()
		wRuntime.EventsEmit(a.ctx, "serversUpdated", servers)
		a.notifyTrayServerListChanged(servers)
	}
	return err
}

func (a *App) CheckServerConnection(id string) error {
	return ReconnectServer(id)
}

func (a *App) RemoveServer(id string) {
	RemoveServer(id)
	servers := ListServers()
	wRuntime.EventsEmit(a.ctx, "serversUpdated", servers)
	a.notifyTrayServerListChanged(servers)
}

func (a *App) ReorderServers(newOrder []string) {
	ReorderServers(newOrder)
}

func (a *App) GetServerList() []Server {
	return ListServers()
}

func (a *App) UpdateServerSettings(id string, opts pcmresample.Options) {
	err := UpdateServerSettings(id, opts)
	if err != nil {
		clientLogger.Errorf("%s", err.Error())
	}
	wRuntime.EventsEmit(a.ctx, "serversUpdated", ListServers())
}

func (a *App) ConnectToAudioServer(id string) {
	// start connect in background (launches a go routine)
	err := ConnectServer(id)
	if err != nil {
		clientLogger.Errorf("%s", err.Error())
	}
}

func (a *App) DisconnetFromAudioServer(id string) {
	err := DisconnectServer(id)
	if err != nil {
		clientLogger.Errorf("%s", err.Error())
	}
}

var serverIsOn = false

func (a *App) GetServerEnabled() bool {
	return serverIsOn
}

func (a *App) EnableServer() {
	serverIsOn = true
	wRuntime.EventsEmit(a.ctx, "updateServerStatus", serverIsOn)

	// Notify systray to update menu label
	select {
	case a.serverStatusChan <- true:
	default:
	}

	// Start server in a separate goroutine to avoid blocking
	go startServer(serverConfig.Port)
}

func (a *App) DisableServer() {
	stopServer()
	serverIsOn = false
	wRuntime.EventsEmit(a.ctx, "updateServerStatus", serverIsOn)

	// Notify systray to update menu label
	select {
	case a.serverStatusChan <- false:
	default:
	}
}

func (a *App) ListAudioDevices() ([]wcatools.AudioConfig, error) {
	return wcatools.ListDevices()
}

func (a *App) AddPlaybackDevice(serverID, deviceID string) error {
	err := AddPlaybackDevice(serverID, deviceID)
	if err == nil {
		wRuntime.EventsEmit(a.ctx, "serversUpdated", ListServers())
	}
	return err
}

func (a *App) RemovePlaybackDevice(serverID, deviceID string) error {
	err := RemovePlaybackDevice(serverID, deviceID)
	if err == nil {
		wRuntime.EventsEmit(a.ctx, "serversUpdated", ListServers())
	}
	return err
}

func (a *App) UpdatePlaybackDevice(serverID, deviceID string, enabled bool, volume float32) error {
	err := UpdatePlaybackDevice(serverID, deviceID, enabled, volume)
	if err == nil {
		wRuntime.EventsEmit(a.ctx, "serversUpdated", ListServers())
	}
	return err
}

func (a *App) SetServerRemoteDevice(serverID, deviceID string) error {
	err := SetServerRemoteDevice(serverID, deviceID)
	if err == nil {
		wRuntime.EventsEmit(a.ctx, "serversUpdated", ListServers())
	}
	return err
}
