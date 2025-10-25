// Package firecrawl implements the Firecrawl Search and Scrape provider APIs.
package firecrawl

import (
	"time"
)

const DEFAULT_BASE_URL = "https://api.firecrawl.dev/"

// SearchRequest contains the provider-neutral search fields supported by Firecrawl.
type SearchRequest struct {
	Query           string
	Limit           int
	IncludeDomains  []string
	ExcludeDomains  []string
	PublishedAfter  *time.Time
	PublishedBefore *time.Time
}

// SearchResponse is a normalized Firecrawl Search response.
type SearchResponse struct {
	Results []SearchResult
}

// SearchResult is one Firecrawl search result.
type SearchResult struct {
	Rank    int
	URL     string
	Title   string
	Snippet string
}

// ContentRequest identifies one public URL to retrieve through Firecrawl Scrape.
type ContentRequest struct {
	URL          string
	DocumentTTL  time.Duration
	ForceRefresh bool
}

// ContentResponse is a normalized Firecrawl Scrape response.
type ContentResponse struct {
	URL     string
	Title   string
	Content string
}
