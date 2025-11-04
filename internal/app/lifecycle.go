package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/LumivoxAI/webrelay/internal/cache"
)

// Shutdown stops HTTP traffic, then background cache work, before closing SQLite.
func Shutdown(ctx context.Context, server *http.Server, cancelCleanup context.CancelFunc, cleanupDone <-chan struct{}, store *cache.Store) error {
	shutdownErr := server.Shutdown(ctx)
	if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
		_ = server.Close()
	}

	cancelCleanup()
	<-cleanupDone
	closeErr := store.Close()
	if errors.Is(shutdownErr, http.ErrServerClosed) {
		shutdownErr = nil
	}
	return errors.Join(shutdownErr, closeErr)
}

// IsLoopbackListenAddress reports whether an HTTP listen address stays on loopback.
func IsLoopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addressIP := net.ParseIP(host)
	return addressIP != nil && addressIP.IsLoopback()
}
