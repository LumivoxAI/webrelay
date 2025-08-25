package config

import "fmt"

// SearchConfig controls the public search operation.
type SearchConfig struct {
	DefaultLimit int      `yaml:"default_limit"`
	MaxLimit     int      `yaml:"max_limit"`
	Providers    []string `yaml:"providers"`
}

// DefaultSearchConfig returns portable search limits and provider order.
func DefaultSearchConfig() SearchConfig {
	return SearchConfig{
		DefaultLimit: 10,
		MaxLimit:     20,
		Providers:    []string{"exa", "brave"},
	}
}

// Validate checks public limits and allowed search providers.
func (c SearchConfig) Validate() error {
	if c.DefaultLimit < 1 || c.MaxLimit < c.DefaultLimit || c.MaxLimit > 20 {
		return fmt.Errorf("search limits must be positive, ordered, and at most 20")
	}
	return validateProviderOrder("search.providers", c.Providers, map[string]bool{"exa": true, "brave": true})
}
