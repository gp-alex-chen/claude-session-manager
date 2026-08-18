package main

// Claude 会话管理 - Wails 内嵌终端版（路线 3 demo）
// 后端：Go + ConPTY；前端：xterm.js；单 exe（WebView2 运行时）

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "Claude 会话管理（内嵌终端）",
		Width:     1180,
		Height:    800,
		MinWidth:  800,
		MinHeight: 560,
		// 窗口/任务栏图标来自 exe 资源（assets/icon.rc，ID 必须为 3：
		// Wails 用 winc.AppIconID=3 从 exe 加载窗口图标；ID 1 只对
		// 资源管理器有效，标题栏会回退默认图标）
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 15, B: 15, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
