package app

import (
	"net/http"
	"time"

	"github.com/LumivoxAI/webrelay/internal/httpapi"
	"go.uber.org/zap"
)

// Config is a temporary runtime configuration until YAML loading is implemented.
type Config struct {
	Listen         string
	RequestTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{
		Listen:         "127.0.0.1:8080",
		RequestTimeout: 35 * time.Second,
	}
}

// NewServer creates the HTTP server with the base public API pipeline.
func NewServer(config Config, logger *zap.Logger) *http.Server {
	return &http.Server{
		Addr:              config.Listen,
		Handler:           httpapi.NewHandler(logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       config.RequestTimeout,
		WriteTimeout:      config.RequestTimeout,
		IdleTimeout:       60 * time.Second,
	}
}
