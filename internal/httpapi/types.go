package httpapi

import "time"

// Readiness is the public response body for GET /health/ready.
type Readiness struct {
	Status           string            `json:"status"`
	SearchProviders  map[string]string `json:"search_providers"`
	ContentProviders map[string]string `json:"content_providers"`
}

// SearchRequest is the public request body for POST /v1/search.
type SearchRequest struct {
	Query           string     `json:"query"`
	Limit           *int       `json:"limit,omitempty"`
	IncludeDomains  []string   `json:"include_domains,omitempty"`
	ExcludeDomains  []string   `json:"exclude_domains,omitempty"`
	PublishedAfter  *time.Time `json:"published_after,omitempty"`
	PublishedBefore *time.Time `json:"published_before,omitempty"`
	ForceRefresh    bool       `json:"force_refresh,omitempty"`
}

// FetchRequest is the public request body for POST /v1/fetch.
type FetchRequest struct {
	URL          string `json:"url"`
	ForceRefresh bool   `json:"force_refresh,omitempty"`
}

// SearchResponse is the public response body for a successful search.
type SearchResponse struct {
	SearchID  string         `json:"search_id"`
	Query     string         `json:"query"`
	Provider  string         `json:"provider"`
	Cached    bool           `json:"cached"`
	CreatedAt time.Time      `json:"created_at"`
	ExpiresAt time.Time      `json:"expires_at"`
	Results   []SearchResult `json:"results"`
}

// SearchResult is one result returned by a search provider.
type SearchResult struct {
	ID          string     `json:"id"`
	Rank        int        `json:"rank"`
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	Snippet     string     `json:"snippet"`
	PublishedAt *time.Time `json:"published_at"`
}

// ContentResponse is the public response body for document content.
type ContentResponse struct {
	DocumentID         string    `json:"document_id"`
	ResultID           *string   `json:"result_id"`
	URL                string    `json:"url"`
	Title              string    `json:"title"`
	Content            string    `json:"content"`
	ContentIsUntrusted bool      `json:"content_is_untrusted"`
	Offset             int       `json:"offset"`
	ReturnedChars      int       `json:"returned_chars"`
	TotalChars         int       `json:"total_chars"`
	Truncated          bool      `json:"truncated"`
	NextOffset         *int      `json:"next_offset"`
	Cached             bool      `json:"cached"`
	SearchProvider     *string   `json:"search_provider"`
	ContentProvider    string    `json:"content_provider"`
	FetchedAt          time.Time `json:"fetched_at"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// Attempt describes a failed provider attempt without leaking upstream details.
type Attempt struct {
	Provider string `json:"provider"`
	Reason   string `json:"reason"`
}

type errorResponse struct {
	Error Error `json:"error"`
}

// Error is the stable public API error representation.
type Error struct {
	Code      Code      `json:"code"`
	Message   string    `json:"message"`
	RequestID string    `json:"request_id"`
	Attempts  []Attempt `json:"attempts,omitempty"`
}
