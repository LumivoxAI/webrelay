package main

import (
	"errors"
	"net/http"
	"os"

	"github.com/LumivoxAI/webrelay/internal/app"
	"go.uber.org/zap"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer func() { _ = logger.Sync() }()

	server := app.NewServer(app.DefaultConfig(), logger)
	logger.Info("starting HTTP server", zap.String("listen", server.Addr))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server stopped unexpectedly", zap.Error(err))
		os.Exit(1)
	}
}
