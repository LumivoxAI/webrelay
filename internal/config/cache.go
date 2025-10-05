package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CacheConfig controls the persistent SQLite cache.
type CacheConfig struct {
	Type            string   `yaml:"type"`
	Path            string   `yaml:"path"`
	SearchTTL       Duration `yaml:"search_ttl"`
	DocumentTTL     Duration `yaml:"document_ttl"`
	CleanupInterval Duration `yaml:"cleanup_interval"`
	MaxSizeMB       int      `yaml:"max_size_mb"`
}

// DefaultCacheConfig returns the XDG cache location and TTL defaults.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		Type:            "sqlite",
		Path:            DefaultCachePath(),
		SearchTTL:       Duration(30 * time.Minute),
		DocumentTTL:     Duration(6 * time.Hour),
		CleanupInterval: Duration(10 * time.Minute),
		MaxSizeMB:       500,
	}
}

// Validate checks cache limits and verifies that the database path is writable.
func (c CacheConfig) Validate() error {
	if c.Type != "sqlite" {
		return fmt.Errorf("cache.type must be sqlite")
	}
	if strings.TrimSpace(c.Path) == "" {
		return fmt.Errorf("cache.path is required")
	}
	if err := ensureCacheDirectory(c.Path); err != nil {
		return err
	}
	if err := validatePositive("cache.search_ttl", c.SearchTTL.Std()); err != nil {
		return err
	}
	if err := validatePositive("cache.document_ttl", c.DocumentTTL.Std()); err != nil {
		return err
	}
	if err := validatePositive("cache.cleanup_interval", c.CleanupInterval.Std()); err != nil {
		return err
	}
	if c.MaxSizeMB < 1 {
		return fmt.Errorf("cache.max_size_mb must be positive")
	}
	return nil
}

// DefaultCachePath returns the XDG cache database location.
func DefaultCachePath() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".cache", APPLICATION_NAME, "cache.db")
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, APPLICATION_NAME, "cache.db")
}

func ensureCacheDirectory(path string) error {
	if path == ":memory:" {
		return nil
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("inspect cache directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cache directory is not a directory")
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open cache path: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close cache path: %w", err)
	}
	return nil
}
