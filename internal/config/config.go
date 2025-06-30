package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	Server    ServerConfig              `json:"server"`
	Database  DatabaseConfig            `json:"database"`
	Providers map[string]ProviderConfig `json:"providers"`
}

type ServerConfig struct {
	Address string `json:"address"`
}

type DatabaseConfig struct {
	Path string `json:"path"`
}

type ProviderConfig struct {
	Enabled      bool   `json:"enabled"`
	APIKeyEnv    string `json:"api_key_env"`
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
		cfg.Database.Path = "./webrelay.sqlite"
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}

	return cfg, nil
}

func (p ProviderConfig) APIKey() string {
	if p.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(p.APIKeyEnv)
}
