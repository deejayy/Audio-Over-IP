package main

import (
	"AuOvIP/pcmresample"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// PersistedServer represents the data we want to save to disk
type PersistedServer struct {
	ID              string
	Addr            string
	Hostname        string
	ResampleOpts    pcmresample.Options
	PlaybackDevices []PlaybackDevice `json:"playbackDevices"`
	RemoteDeviceID  string           `json:"remoteDeviceID"`
}

type PlaybackDevice struct {
	DeviceID  string  `json:"deviceID"`
	Name      string  `json:"name"`
	Volume    float32 `json:"volume"`    // 0.0 - 1.0
	Enabled   bool    `json:"enabled"`   // Mute/Unmute
	IsDefault bool    `json:"isDefault"` // If true, tracks system default
}

// ServerListConfigData represents the root structure of Servers.json
type ServerListConfigData struct {
	Servers     []PersistedServer `json:"servers"`
	ServerOrder []string          `json:"serverOrder"`
}

// ServerConfig represents settings for the Sender mode
type ServerConfig struct {
	Port string `json:"port"`
}

// ClientConfig represents settings for the Receiver mode
type ClientConfig struct {
	DefaultResampleOpts pcmresample.Options `json:"defaultResampleOpts"`
}

const (
	vendorName        = "ElTheLedge"
	appName           = "AudioOverIP"
	serversConfigName = "Servers.json"
	serverConfigName  = "ServerSettings.json"
	clientConfigName  = "ClientSettings.json"
)

var (
	configMu sync.Mutex
	// Defaults
	serverConfig = ServerConfig{
		Port: "8080",
	}
	clientConfig = ClientConfig{
		DefaultResampleOpts: pcmresample.Options{
			Method:              pcmresample.InterpSinc,
			Quality:             20,
			MaxQuality:          100,
			MaxThreads:          0, //0 = Auto
			IncludeLFEInDownmix: true,
		},
	}
)

func GetConfigDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(configDir, vendorName, appName)
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", err
	}
	return path, nil
}

func getFilePath(filename string) (string, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filename), nil
}

func LoadConfig() error {
	_ = LoadServerConfig()
	_ = LoadClientConfig()

	// Load Servers.json
	path, err := getFilePath(serversConfigName)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // No config file yet, start fresh
	}
	if err != nil {
		return err
	}

	var cfg ServerListConfigData
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}

	serversMu.Lock()
	defer serversMu.Unlock()

	servers = make(map[string]*Server)
	serverOrder = cfg.ServerOrder

	// Populate the map
	for _, p := range cfg.Servers {
		s := &Server{
			ID:              p.ID,
			Addr:            p.Addr,
			Hostname:        p.Hostname,
			Status:          connStatuses.Idle,
			ResampleOpts:    p.ResampleOpts,
			PlaybackDevices: p.PlaybackDevices,
			RemoteDeviceID:  p.RemoteDeviceID,
			settingsCh:      make(chan settingsRequest, 1),
			cancel:          nil,
			players:         make(map[string]*DevicePlayer),
		}

		// If no devices, add default
		if len(s.PlaybackDevices) == 0 {
			// For now, we leave it empty and handle "empty means default" logic in backend.go
		}
		servers[p.ID] = s
	}

	// Validate order integrity
	if len(serverOrder) != len(servers) {
		serverOrder = make([]string, 0, len(servers))
		for id := range servers {
			serverOrder = append(serverOrder, id)
		}
	}

	return nil
}

func SaveServerListConfig() error {
	configMu.Lock()
	defer configMu.Unlock()

	serversMu.Lock()
	var cfg ServerListConfigData
	cfg.ServerOrder = make([]string, len(serverOrder))
	copy(cfg.ServerOrder, serverOrder)

	cfg.Servers = make([]PersistedServer, 0, len(servers))
	for _, s := range servers {
		cfg.Servers = append(cfg.Servers, PersistedServer{
			ID:              s.ID,
			Addr:            s.Addr,
			Hostname:        s.Hostname,
			ResampleOpts:    s.ResampleOpts,
			PlaybackDevices: s.PlaybackDevices,
			RemoteDeviceID:  s.RemoteDeviceID,
		})
	}
	serversMu.Unlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	path, err := getFilePath(serversConfigName)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func LoadServerConfig() error {
	path, err := getFilePath(serverConfigName)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &serverConfig)
}

func SaveServerConfig() error {
	configMu.Lock()
	defer configMu.Unlock()
	data, err := json.MarshalIndent(serverConfig, "", "  ")
	if err != nil {
		return err
	}
	path, err := getFilePath(serverConfigName)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func LoadClientConfig() error {
	path, err := getFilePath(clientConfigName)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &clientConfig)
}

func SaveClientConfig() error {
	configMu.Lock()
	defer configMu.Unlock()
	data, err := json.MarshalIndent(clientConfig, "", "  ")
	if err != nil {
		return err
	}
	path, err := getFilePath(clientConfigName)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
