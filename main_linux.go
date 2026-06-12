//go:build linux

package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"

	"pwdtt-desktop/backend"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/icons/icon.png
var appIcon []byte

//go:embed assets/icons/tree-icon.png
var trayIcon []byte

func main() {
	app := backend.NewApp(trayIcon)

	err := wails.Run(&options.App{
		Title:     "PWDTT",
		Width:     900,
		Height:    600,
		MinWidth:  800,
		MinHeight: 550,
		Frameless: false,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 20, G: 20, B: 22, A: 1},
		OnStartup:        app.Startup,
		OnBeforeClose:    app.OnBeforeClose,
		Bind:             []interface{}{app},
		Linux: &linux.Options{
			ProgramName: "PWDTT",
		},
	})
	if err != nil {
		panic(err)
	}
}
