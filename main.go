package main

import (
	"embed"

	"photoslicer/engine/constants"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "PhotoSlicer v" + constants.Version,
		Width:     baseWindowLayout.Width,
		Height:    baseWindowLayout.Height,
		MinWidth:  baseWindowLayout.MinWidth,
		MinHeight: baseWindowLayout.MinHeight,
		// Shown by the frontend (App.ShowWindow) once the saved theme, language
		// and layout are applied, so nothing visibly changes right after launch.
		StartHidden: true,
		// The native frame is replaced by the title bar in frontend/index.html.
		// Wails keeps the drop shadow and, on Windows 11, the rounded corners.
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 23, A: 255},
		OnStartup:        app.startup,
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			BackdropType:         windows.Auto,
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
