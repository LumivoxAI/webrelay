package main

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/LumivoxAI/webrelay/internal/app"
	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/alecthomas/kong"
	"go.uber.org/zap"
)

type cli struct {
	ConfigPath string `name:"config" short:"c" help:"Path to YAML configuration." type:"path"`
}

func main() {
	var cli cli
	kong.Parse(&cli,
		kong.Name("webrelay"),
		kong.Description("Web Retrieval Gateway."),
	)

	bootstrapLogger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer func() { _ = bootstrapLogger.Sync() }()

	runtimeConfig, err := config.Load(cli.ConfigPath)
	if err != nil {
		bootstrapLogger.Error("Configuration validation failed", zap.Error(err))
		_ = bootstrapLogger.Sync()
		os.Exit(1)
	}

	logger, err := app.NewLogger(runtimeConfig.Logging)
	if err != nil {
		bootstrapLogger.Error("Create logger", zap.Error(err))
		_ = bootstrapLogger.Sync()
		os.Exit(1)
	}
	defer func() { _ = logger.Sync() }()

	cacheStore, err := app.OpenCache(context.Background(), runtimeConfig.Cache)
	if err != nil {
		logger.Error("Open cache", zap.Error(err))
		_ = logger.Sync()
		os.Exit(1)
	}
	defer func() { _ = cacheStore.Close() }()
	cleanupContext, cancelCleanup := context.WithCancel(context.Background())
	defer cancelCleanup()
	go cacheStore.StartCleanupWorker(cleanupContext, runtimeConfig.Cache.CleanupInterval.Std(), logger)

	server, err := app.NewServer(runtimeConfig, cacheStore, logger)
	if err != nil {
		logger.Error("Create HTTP server", zap.Error(err))
		os.Exit(1)
	}
	logger.Info("Starting HTTP server", zap.String("listen", server.Addr))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server stopped unexpectedly", zap.Error(err))
		os.Exit(1)
	}
}
