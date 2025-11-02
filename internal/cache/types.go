// Package cache provides persistent search and document storage.
package cache

import "time"

// SearchEntry is a cached provider search response.
type SearchEntry struct {
	ID              string
	Key             string
	OriginalQuery   string
	NormalizedQuery string
	Parameters      []byte
	Provider        string
	CreatedAt       time.Time
	ExpiresAt       time.Time
	Results         []SearchResult
}

// SearchResult is one result belonging to a cached search entry.
type SearchResult struct {
	ID             string
	SearchID       string
	Rank           int
	URL            string
	NormalizedURL  string
	Title          string
	Snippet        string
	PublishedAt    *time.Time
	SearchProvider string
}

// Document is content cached by its normalized public URL.
type Document struct {
	ID              string
	NormalizedURL   string
	OriginalURL     string
	Title           string
	Content         string
	Provider        string
	SourceMediaType *string
	ContentHash     string
	FetchedAt       time.Time
	ExpiresAt       time.Time
	LastAccessedAt  time.Time
}
