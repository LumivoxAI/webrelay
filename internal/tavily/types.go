// Package tavily implements the Tavily Search and Extract provider APIs.
package tavily

import "time"

const DEFAULT_BASE_URL = "https://api.tavily.com/"

// SearchRequest contains the provider-neutral search fields supported by Tavily.
type SearchRequest struct {
	Query           string
	Limit           int
	IncludeDomains  []string
	ExcludeDomains  []string
	PublishedAfter  *time.Time
	PublishedBefore *time.Time
}

// SearchResponse is a normalized Tavily Search response.
type SearchResponse struct {
	Results []SearchResult
}

// SearchResult is one Tavily search result.
type SearchResult struct {
	Rank        int
	URL         string
	Title       string
	Snippet     string
	PublishedAt *time.Time
}

// ContentRequest identifies one public URL to retrieve through Tavily Extract.
type ContentRequest struct {
	URL string
}

// ContentResponse is a normalized Tavily Extract response.
type ContentResponse struct {
	URL     string
	Content string
}
