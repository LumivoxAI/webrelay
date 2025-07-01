package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Server    ServerConfig              `json:"server"`
	Database  DatabaseConfig            `json:"database"`
	Proxy     ProxyConfig               `json:"proxy"`
	Providers map[string]ProviderConfig `json:"providers"`
}

type ServerConfig struct {
	Address string `json:"address"`
}

type DatabaseConfig struct {
	Path string `json:"path"`
}

type ProxyConfig struct {
	URL string `json:"url"`
}

type ProviderConfig struct {
	Enabled      bool   `json:"enabled"`
	APIKey       string `json:"api_key"`
	MonthlyLimit int64  `json:"monthly_limit"`
	RPMLimit     int64  `json:"rpm_limit"`
	Priority     int    `json:"priority"`
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	if cfg.Server.Address == "" {
		cfg.Server.Address = "127.0.0.1:8080"
	}
	if cfg.Database.Path == "" {
		path, err := defaultDatabasePath()
		if err != nil {
			return Config{}, err
		}
		cfg.Database.Path = path
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}

	return cfg, nil
}

func defaultDatabasePath() (string, error) {
	if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "webrelay", "db.sqlite"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "webrelay", "db.sqlite"), nil
}
