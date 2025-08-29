package app

import (
	"net/http"
	"time"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/httpapi"
	"go.uber.org/zap"
)

// NewServer creates the HTTP server with the base public API pipeline.
func NewServer(config config.Config, logger *zap.Logger) *http.Server {
	return &http.Server{
		Addr:              config.Server.Listen,
		Handler:           httpapi.NewHandler(logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       config.Server.RequestTimeout.Std(),
		WriteTimeout:      config.Server.RequestTimeout.Std(),
		IdleTimeout:       60 * time.Second,
	}
}
