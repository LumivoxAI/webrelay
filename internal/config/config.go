// Package config loads and validates the gateway runtime configuration.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const APPLICATION_NAME = "web-retrieval-gateway"

var environmentReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Config contains all runtime settings of the gateway.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Search    SearchConfig    `yaml:"search"`
	Content   ContentConfig   `yaml:"content"`
	Providers ProvidersConfig `yaml:"providers"`
	Cache     CacheConfig     `yaml:"cache"`
	Logging   LoggingConfig   `yaml:"logging"`

	providerIssues map[string]string
}

// Default returns the complete configuration before YAML overrides are applied.
func Default() Config {
	return Config{
		Server:    DefaultServerConfig(),
		Search:    DefaultSearchConfig(),
		Content:   DefaultContentConfig(),
		Providers: DefaultProvidersConfig(),
		Cache:     DefaultCacheConfig(),
		Logging:   DefaultLoggingConfig(),
	}
}

// Validate checks global settings and verifies that both routing paths are available.
func (c *Config) Validate() error {
	if err := c.Server.Validate(); err != nil {
		return err
	}
	if err := c.Search.Validate(); err != nil {
		return err
	}
	if err := c.Content.Validate(); err != nil {
		return err
	}
	if err := c.Cache.Validate(); err != nil {
		return err
	}
	if err := c.Logging.Validate(); err != nil {
		return err
	}

	c.providerIssues = c.Providers.Validate(c.Content.MaxDocumentChars)
	if !c.hasAvailable(c.Search.Providers) {
		return fmt.Errorf("no configured search provider")
	}
	if !c.hasAvailable(c.Content.Providers) {
		return fmt.Errorf("no configured content provider")
	}
	return nil
}

// ProviderIssue returns the safe reason why a provider cannot be used.
func (c Config) ProviderIssue(name string) string {
	return c.providerIssues[name]
}

// ProviderAvailable reports whether a validated provider can serve requests.
func (c Config) ProviderAvailable(name string) bool {
	return c.providerIssues[name] == ""
}

func (c Config) hasAvailable(providers []string) bool {
	for _, provider := range providers {
		if c.ProviderAvailable(provider) {
			return true
		}
	}
	return false
}

// DefaultConfigPath returns the XDG configuration location.
func DefaultConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".config", APPLICATION_NAME, "config.yaml")
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, APPLICATION_NAME, "config.yaml")
}

// Load reads, expands, and validates a YAML configuration file.
func Load(path string) (Config, error) {
	if path == "" {
		path = DefaultConfigPath()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}

	cfg := Default()
	decoder := yamlDecoder(bytes.NewReader(substituteEnvironment(raw)))
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func substituteEnvironment(raw []byte) []byte {
	return environmentReference.ReplaceAllFunc(raw, func(match []byte) []byte {
		name := string(match[2 : len(match)-1])
		return []byte(os.Getenv(name))
	})
}
