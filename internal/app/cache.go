package app

import (
	"context"

	"github.com/LumivoxAI/webrelay/internal/cache"
	"github.com/LumivoxAI/webrelay/internal/config"
)

// OpenCache opens the persistent cache before the HTTP server accepts requests.
func OpenCache(ctx context.Context, cfg config.CacheConfig) (*cache.Store, error) {
	return cache.Open(ctx, cfg)
}
