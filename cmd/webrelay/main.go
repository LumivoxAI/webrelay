package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/LumivoxAI/webrelay/internal/app"
	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/alecthomas/kong"
	"go.uber.org/zap"
)

type cli struct {
	ConfigPath string        `name:"config" short:"c" help:"Path to YAML configuration." type:"path"`
	Host       *string       `help:"Override the server host from configuration."`
	Port       *uint16       `help:"Override the server port from configuration."`
	Serve      struct{}      `cmd:"" default:"1" hidden:""`
	Export     exportCommand `cmd:"" help:"Export commands."`
}

type exportCommand struct {
	Config exportConfigCommand `cmd:"" help:"Write the complete default configuration."`
}

type exportConfigCommand struct {
	Output string `name:"output" short:"o" help:"Write to a new file instead of stdout." type:"path"`
}

func main() {
	var cli cli
	kongContext := kong.Parse(&cli,
		kong.Name("webrelay"),
		kong.Description("Web Retrieval Gateway."),
	)
	if kongContext.Command() == "export config" {
		if err := exportDefaultConfig(cli.Export.Config.Output); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

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
	runtimeConfig.Server, err = runtimeConfig.Server.WithListenOverrides(cli.Host, cli.Port)
	if err != nil {
		bootstrapLogger.Error("Server address validation failed", zap.Error(err))
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
	cleanupContext, cancelCleanup := context.WithCancel(context.Background())
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		cacheStore.StartCleanupWorker(cleanupContext, runtimeConfig.Cache.CleanupInterval.Std(), logger)
	}()

	server, err := app.NewServer(runtimeConfig, cacheStore, logger)
	if err != nil {
		logger.Error("Create HTTP server", zap.Error(err))
		cancelCleanup()
		<-cleanupDone
		_ = cacheStore.Close()
		os.Exit(1)
	}
	if !app.IsLoopbackListenAddress(server.Addr) {
		logger.Warn("HTTP API is exposed beyond loopback without authentication", zap.String("listen", server.Addr))
	}
	logger.Info("Starting HTTP server", zap.String("listen", server.Addr))
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()

	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	unexpectedStop := false
	select {
	case <-signalContext.Done():
		logger.Info("Shutdown signal received")
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			unexpectedStop = true
			logger.Error("HTTP server stopped unexpectedly", zap.Error(err))
		}
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), runtimeConfig.Server.ShutdownTimeout.Std())
	defer cancelShutdown()
	if err := app.Shutdown(shutdownContext, server, cancelCleanup, cleanupDone, cacheStore); err != nil {
		logger.Error("Graceful shutdown failed", zap.Error(err))
		_ = logger.Sync()
		os.Exit(1)
	}
	if unexpectedStop {
		_ = logger.Sync()
		os.Exit(1)
	}
}

func exportDefaultConfig(outputPath string) error {
	document, err := config.ExportDefaultYAML()
	if err != nil {
		return err
	}
	if outputPath == "" {
		_, err := os.Stdout.Write(document)
		return err
	}
	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create configuration export: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(document); err != nil {
		return fmt.Errorf("write configuration export: %w", err)
	}
	return nil
}
