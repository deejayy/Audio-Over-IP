package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
)

//go:embed all:frontend/dist
var assets embed.FS

// Create an instance of the app structure
var app = NewApp()

var serverLogger *Logger
var clientLogger *Logger

func main() {
	var err error

	// Initialize loggers (1MB size limit)
	serverLogger, err = InitLogger("logs/server.log", LevelDebug, 1024*1024)
	if err != nil {
		log.Println("Could not init serverLogger:", err)
	}
	clientLogger, err = InitLogger("logs/client.log", LevelDebug, 1024*1024)
	if err != nil {
		log.Println("Could not init clientLogger:", err)
	}

	// Create application with options
	err = wails.Run(&options.App{
		Title:            "Audio Over IP",
		Width:            1280,
		Height:           720,
		MinWidth:         384,
		MinHeight:        216,
		Assets:           assets,
		Frameless:        true,
		CSSDragProperty:  "widows",
		CSSDragValue:     "1",
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
		OnDomReady: app.domReady,
	})

	if err != nil {
		clientLogger.Errorf("wails run failed: %v", err)
		os.Exit(1)
	}
}
