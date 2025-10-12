// Package brave implements the Brave Web Search provider API.
package brave

import "time"

const DEFAULT_ENDPOINT = "https://api.search.brave.com/res/v1/web/search"

// SearchRequest contains the provider-neutral search fields supported by Brave.
type SearchRequest struct {
	Query           string
	Limit           int
	IncludeDomains  []string
	ExcludeDomains  []string
	PublishedAfter  *time.Time
	PublishedBefore *time.Time
}

// SearchResponse is a normalized Brave Web Search response.
type SearchResponse struct {
	Results []SearchResult
}

// SearchResult is one Brave Web Search result.
type SearchResult struct {
	Rank        int
	URL         string
	Title       string
	Snippet     string
	PublishedAt *time.Time
}
