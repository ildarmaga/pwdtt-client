package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"pwdtt-desktop/backend"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/icons/icon.png
var appIcon []byte

//go:embed assets/icons/tray-icon.png
var trayIcon []byte

//go:embed assets/wintun.dll
var wintunDLL []byte

func main() {

	backend.InitWintun(wintunDLL)
	app := backend.NewApp(trayIcon)

	err := wails.Run(&options.App{
		Title:         "WDTT",
		Width:         680,
		Height:        750,
		MinWidth:      680,
		MinHeight:     750,
		MaxWidth:      680,
		MaxHeight:     750,
		DisableResize: true,
		Frameless:     false,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 20, G: 20, B: 22, A: 1},
		OnStartup:        app.Startup,
		OnBeforeClose:    app.OnBeforeClose,
		Bind:             []interface{}{app},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		panic(err)
	}
}
