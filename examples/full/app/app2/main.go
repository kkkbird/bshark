package main

import (
	"context"

	"github.com/kkkbird/bshark"
	"github.com/kkkbird/bshark/examples/full/pkg/db"
	"github.com/kkkbird/bshark/examples/full/pkg/httpsrv"
)

const (
	appName = "app2"
	addr    = ":8082"
)

func initDB(ctx context.Context) (bshark.CleanFunc, error) {
	db.Init(ctx, appName)

	return nil, nil
}

func runHTTPServer(ctx context.Context) error {
	return httpsrv.Run(ctx, appName, addr)
}

func main() {
	bshark.New(appName).
		AddInitStage("initDB", initDB).
		AddDaemons(runHTTPServer).
		Run()
}
