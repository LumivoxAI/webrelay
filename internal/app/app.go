package app

import (
	"net/http"
	"time"

	"github.com/LumivoxAI/webrelay/internal/cache"
	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/httpapi"
	"go.uber.org/zap"
)

// NewServer creates the HTTP server and its configured business workflows.
func NewServer(config config.Config, store *cache.Store, logger *zap.Logger) (*http.Server, error) {
	searchWorkflow, contentWorkflow, manager, err := NewWorkflows(config, store, logger)
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Addr: config.Server.Listen,
		Handler: httpapi.NewHandler(logger, httpapi.Dependencies{
			Search:              searchWorkflow,
			Content:             contentWorkflow,
			Health:              NewReadinessChecker(config, store, manager),
			MaxRequestBodyBytes: int64(config.Server.MaxRequestBodyBytes),
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       config.Server.RequestTimeout.Std(),
		WriteTimeout:      config.Server.RequestTimeout.Std(),
		IdleTimeout:       60 * time.Second,
	}, nil
}
