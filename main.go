package main

import (
	"embed"
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var frontend embed.FS

//go:embed build/windows/icon.ico
var appIconBytes []byte

func main() {
	app := NewApp()

	// 从 embed.FS 创建子文件系统，根目录指向 frontend/dist
	distFS, err := fs.Sub(frontend, "frontend/dist")
	if err != nil {
		log.Fatal("frontend/dist not found:", err)
	}

	err = wails.Run(&options.App{
		Title:  "Kiro Client",
		Width:  1200,
		Height: 728,
		AssetServer: &assetserver.Options{
			Assets: distFS,
		},
		HideWindowOnClose:  true,
		OnStartup:          app.startup,
		OnShutdown:         app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})

	if err != nil {
		log.Fatal("启动失败:", err)
	}
}