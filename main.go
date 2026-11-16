package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/24DSO-lab/wallet/internal/bootstrap"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	runtime, err := bootstrap.New()
	if err != nil {
		log.Fatal(err)
	}

	err = wails.Run(&options.App{
		Title:       "Wallet",
		Width:       1100,
		Height:      760,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup:   runtime.App.Startup,
		OnShutdown:  runtime.Shutdown,
		Bind:        []any{runtime.App},
	})
	if err != nil {
		runtime.Shutdown(context.Background())
		log.Fatal(err)
	}
}
