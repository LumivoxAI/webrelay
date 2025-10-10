// Package exa implements the Exa Search and Contents provider API.
package exa

import "time"

const DEFAULT_BASE_URL = "https://api.exa.ai/"

// SearchRequest contains the provider-neutral search fields supported by Exa.
type SearchRequest struct {
	Query           string
	Limit           int
	IncludeDomains  []string
	ExcludeDomains  []string
	PublishedAfter  *time.Time
	PublishedBefore *time.Time
}

// SearchResponse is a normalized Exa Search response.
type SearchResponse struct {
	Results []SearchResult
}

// SearchResult is one search result. EmbeddedContent is deliberately separate
// from the public search representation so it can be stored in document cache.
type SearchResult struct {
	Rank            int
	URL             string
	Title           string
	Snippet         string
	PublishedAt     *time.Time
	EmbeddedContent string
}

// ContentRequest identifies one public URL to retrieve through Exa Contents.
type ContentRequest struct {
	URL string
}

// ContentResponse is a normalized Exa Contents response.
type ContentResponse struct {
	URL             string
	Title           string
	Content         string
	SourceMediaType *string
}
