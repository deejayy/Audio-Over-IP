package main

import (
	"context"
	"os/exec"

	"AuOvIP/pcmresample"
	"AuOvIP/wcatools"

	wRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx context.Context
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
	return &App{}
}

// startup is called at application startup
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	_ = LoadConfig()

	InitDeviceMonitor()

	for _, s := range ListServers() {
		go func(id string) {
			_ = ReconnectServer(id)
		}(s.ID)
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
	return false
}

// shutdown is called at application termination
func (a *App) shutdown(ctx context.Context) {
	// Perform teardown here
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
		wRuntime.EventsEmit(a.ctx, "serversUpdated", ListServers())
	}
	return err
}

func (a *App) CheckServerConnection(id string) error {
	return ReconnectServer(id)
}

func (a *App) RemoveServer(id string) {
	RemoveServer(id)
	wRuntime.EventsEmit(a.ctx, "serversUpdated", ListServers())
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
	startServer(serverConfig.Port)
}

func (a *App) DisableServer() {
	stopServer()
	serverIsOn = false
	wRuntime.EventsEmit(a.ctx, "updateServerStatus", serverIsOn)
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
