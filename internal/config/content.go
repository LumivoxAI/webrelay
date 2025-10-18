package config

import "fmt"

// ContentConfig controls document chunking and content provider order.
type ContentConfig struct {
	DefaultChunkChars int      `yaml:"default_chunk_chars"`
	MaxChunkChars     int      `yaml:"max_chunk_chars"`
	MaxDocumentChars  int      `yaml:"max_document_chars"`
	Providers         []string `yaml:"providers"`
}

// DefaultContentConfig returns bounded content limits.
func DefaultContentConfig() ContentConfig {
	return ContentConfig{
		DefaultChunkChars: 12000,
		MaxChunkChars:     50000,
		MaxDocumentChars:  500000,
		Providers:         []string{"tinyfish", "markdown_new", "tavily", "exa", "firecrawl"},
	}
}

// Validate checks chunking limits and allowed content providers.
func (c ContentConfig) Validate() error {
	if c.DefaultChunkChars < 1 || c.MaxChunkChars < c.DefaultChunkChars || c.MaxDocumentChars < c.MaxChunkChars {
		return fmt.Errorf("content limits must be positive and ordered")
	}
	return validateProviderOrder("content.providers", c.Providers, map[string]bool{"tinyfish": true, "markdown_new": true, "tavily": true, "exa": true, "firecrawl": true})
}
