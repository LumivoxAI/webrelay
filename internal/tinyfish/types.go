// Package tinyfish implements the TinyFish Search and Fetch provider APIs.
package tinyfish

import "time"

const (
	DEFAULT_SEARCH_ENDPOINT = "https://api.search.tinyfish.ai"
	DEFAULT_FETCH_ENDPOINT  = "https://api.fetch.tinyfish.ai"
)

// SearchRequest contains the provider-neutral search fields supported by TinyFish.
type SearchRequest struct {
	Query           string
	Limit           int
	IncludeDomains  []string
	ExcludeDomains  []string
	PublishedAfter  *time.Time
	PublishedBefore *time.Time
}

// SearchResponse is a normalized TinyFish Search response.
type SearchResponse struct {
	Results []SearchResult
}

// SearchResult is one TinyFish Search result.
type SearchResult struct {
	Rank        int
	URL         string
	Title       string
	Snippet     string
	PublishedAt *time.Time
}

// ContentRequest identifies one public URL to retrieve through TinyFish Fetch.
type ContentRequest struct {
	URL          string
	DocumentTTL  time.Duration
	ForceRefresh bool
}

// ContentResponse is a normalized TinyFish Fetch response.
type ContentResponse struct {
	URL         string
	Title       string
	Content     string
	PublishedAt *time.Time
}
