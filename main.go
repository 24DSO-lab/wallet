package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/24DSO-lab/wallet/app"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	a := app.New()

	err := wails.Run(&options.App{
		Title:       "Wallet",
		Width:       1100,
		Height:      760,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup:   a.Startup,
		OnShutdown:  a.Shutdown,
		Bind:        []any{a},
	})
	if err != nil {
		log.Fatal(err)
	}
}
