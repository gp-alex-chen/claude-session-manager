package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/gp-alex-chen/claude-session-manager/internal/app"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app.CleanupUpdateArtifacts()
	dataDir := state.DefaultDir()
	if dataDir == "" {
		log.Fatal("无法确定 LocalAppData 应用数据目录")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatal("无法创建应用数据目录: ", err)
	}
	webviewPath := filepath.Join(dataDir, "WebView2")
	a := app.NewApp()
	if err := wails.Run(&options.App{
		Title:            "Claude 会话管理（内嵌终端）",
		Width:            1180,
		Height:           800,
		MinWidth:         800,
		MinHeight:        560,
		AssetServer:      &assetserver.Options{Assets: assets},
		Windows:          &windows.Options{WebviewUserDataPath: webviewPath},
		BackgroundColour: &options.RGBA{R: 15, G: 15, B: 15, A: 255},
		OnStartup:        a.Startup,
		OnShutdown:       a.Shutdown,
		Bind:             []interface{}{a},
	}); err != nil {
		log.Fatal(err)
	}
}
