package main

import (
	"embed"
	"log"

	"github.com/gp-alex-chen/claude-session-manager/internal/app"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app.CleanupUpdateArtifacts()
	a := app.NewApp()
	if err := wails.Run(&options.App{
		Title:            "Claude 会话管理（内嵌终端）",
		Width:            1180,
		Height:           800,
		MinWidth:         800,
		MinHeight:        560,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 15, G: 15, B: 15, A: 255},
		OnStartup:        a.Startup,
		OnShutdown:       a.Shutdown,
		Bind:             []interface{}{a},
	}); err != nil {
		log.Fatal(err)
	}
}
