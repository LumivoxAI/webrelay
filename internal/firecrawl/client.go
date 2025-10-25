package firecrawl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"go.uber.org/zap"
)

const MAX_RESPONSE_BYTES = 8 * 1024 * 1024

// Client calls Firecrawl Search and Scrape using isolated provider HTTP clients.
type Client struct {
	baseURL    *url.URL
	searchHTTP *http.Client
	scrapeHTTP *http.Client
	config     config.FirecrawlConfig
	metrics    *provider.Metrics
	logger     *zap.Logger
	now        func() time.Time
}

// New creates a Firecrawl client for the production API endpoint.
func New(settings config.FirecrawlConfig, searchHTTP, scrapeHTTP *http.Client, metrics *provider.Metrics, logger *zap.Logger) (*Client, error) {
	return NewAtURL(DEFAULT_BASE_URL, settings, searchHTTP, scrapeHTTP, metrics, logger)
}

// NewAtURL creates a Firecrawl client at a supplied base URL, primarily for tests.
func NewAtURL(rawBaseURL string, settings config.FirecrawlConfig, searchHTTP, scrapeHTTP *http.Client, metrics *provider.Metrics, logger *zap.Logger) (*Client, error) {
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("parse Firecrawl base URL")
	}
	if searchHTTP == nil || scrapeHTTP == nil {
		return nil, fmt.Errorf("Firecrawl HTTP clients are required")
	}
	if metrics == nil {
		metrics = provider.NewMetrics(nil)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Client{
		baseURL:    baseURL,
		searchHTTP: searchHTTP,
		scrapeHTTP: scrapeHTTP,
		config:     settings,
		metrics:    metrics,
		logger:     logger,
		now:        time.Now,
	}, nil
}

// Search retrieves and normalizes Firecrawl web search results.
func (c *Client) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	payload := searchRequestPayload{
		Query:          request.Query,
		Limit:          request.Limit,
		IncludeDomains: request.IncludeDomains,
		ExcludeDomains: request.ExcludeDomains,
		TBS:            c.tbs(request.PublishedAfter, request.PublishedBefore),
	}
	var upstream searchResponsePayload
	if err := c.call(ctx, c.searchHTTP, "v2/search", payload, &upstream); err != nil {
		return SearchResponse{}, err
	}
	if !upstream.Success {
		return SearchResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Cause: fmt.Errorf("Firecrawl search was unsuccessful")}
	}
	c.metrics.RecordCredits(provider.Key{Provider: provider.FIRECRAWL, Action: provider.SEARCH}, upstream.CreditsUsed)

	results := make([]SearchResult, 0, len(upstream.Data.Web))
	for index, result := range upstream.Data.Web {
		results = append(results, SearchResult{Rank: index + 1, URL: result.URL, Title: result.Title, Snippet: result.Description})
	}
	return SearchResponse{Results: results}, nil
}

// Scrape retrieves Markdown content for one public URL.
func (c *Client) Scrape(ctx context.Context, request ContentRequest) (ContentResponse, error) {
	maxAge := int(request.DocumentTTL.Milliseconds())
	if request.ForceRefresh {
		maxAge = 0
	}
	payload := scrapeRequestPayload{
		URL:             request.URL,
		Formats:         []string{"markdown"},
		OnlyMainContent: true,
		Lockdown:        false,
		MaxAge:          maxAge,
	}
	var upstream scrapeResponsePayload
	if err := c.call(ctx, c.scrapeHTTP, "v2/scrape", payload, &upstream); err != nil {
		return ContentResponse{}, err
	}
	if !upstream.Success {
		return ContentResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Cause: fmt.Errorf("Firecrawl scrape was unsuccessful")}
	}
	c.metrics.RecordCredits(provider.Key{Provider: provider.FIRECRAWL, Action: provider.SCRAPE}, upstream.CreditsUsed)
	if strings.TrimSpace(upstream.Data.Markdown) == "" {
		return ContentResponse{}, &provider.Failure{Reason: provider.REASON_UNAVAILABLE, Cause: fmt.Errorf("Firecrawl returned empty content")}
	}
	resultURL := upstream.Data.Metadata.URL
	if resultURL == "" {
		resultURL = upstream.Data.Metadata.SourceURL
	}
	if resultURL == "" {
		resultURL = request.URL
	}
	return ContentResponse{URL: resultURL, Title: upstream.Data.Metadata.Title, Content: upstream.Data.Markdown}, nil
}

func (c *Client) tbs(after, before *time.Time) string {
	if after == nil {
		return ""
	}
	end := c.now().UTC()
	if before != nil {
		end = before.UTC()
	}
	return "cdr:1,cd_min:" + after.UTC().Format("01/02/2006") + ",cd_max:" + end.Format("01/02/2006")
}

func (c *Client) call(ctx context.Context, client *http.Client, path string, payload, destination any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal Firecrawl request: %w", err)
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Firecrawl request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return classifyTransportError(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, MAX_RESPONSE_BYTES+1))
	if err != nil {
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("read Firecrawl response: %w", err)}
	}
	if len(responseBody) > MAX_RESPONSE_BYTES {
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("Firecrawl response exceeds size limit")}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return c.classifyHTTPError(response.StatusCode, response.Header.Get("Retry-After"))
	}
	if err := json.Unmarshal(responseBody, destination); err != nil {
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("decode Firecrawl response: %w", err)}
	}
	c.logger.Debug("Firecrawl upstream response", zap.Int("upstream_status", response.StatusCode))
	return nil
}

func (c *Client) classifyHTTPError(status int, retryAfter string) error {
	c.logger.Warn("Firecrawl upstream request failed", zap.Int("upstream_status", status))
	failure := &provider.Failure{Cause: fmt.Errorf("Firecrawl returned HTTP %d", status)}
	switch status {
	case http.StatusUnauthorized:
		failure.Reason = provider.REASON_UNAUTHORIZED
	case http.StatusPaymentRequired:
		failure.Reason = provider.REASON_QUOTA
	case http.StatusForbidden:
		failure.Reason = provider.REASON_FORBIDDEN
	case http.StatusRequestTimeout:
		failure.Reason = provider.REASON_TIMEOUT
		failure.Retryable = true
	case http.StatusTooManyRequests:
		failure.Reason = provider.REASON_RATE_LIMITED
		failure.Retryable = true
		failure.Cooldown = retryAfterDuration(retryAfter, c.now())
	case http.StatusInternalServerError, http.StatusBadGateway:
		failure.Reason = provider.REASON_TEMPORARY
		failure.Retryable = true
	default:
		failure.Reason = provider.REASON_TEMPORARY
	}
	return failure
}

func retryAfterDuration(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if resetAt, err := http.ParseTime(value); err == nil && resetAt.After(now) {
		return resetAt.Sub(now)
	}
	return 0
}

func classifyTransportError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &provider.Failure{Reason: provider.REASON_TIMEOUT, Retryable: true, Cause: err}
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return &provider.Failure{Reason: provider.REASON_TIMEOUT, Retryable: true, Cause: err}
	}
	return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: err}
}

type searchRequestPayload struct {
	Query          string   `json:"query"`
	Limit          int      `json:"limit"`
	IncludeDomains []string `json:"includeDomains,omitempty"`
	ExcludeDomains []string `json:"excludeDomains,omitempty"`
	TBS            string   `json:"tbs,omitempty"`
}

type scrapeRequestPayload struct {
	URL             string   `json:"url"`
	Formats         []string `json:"formats"`
	OnlyMainContent bool     `json:"onlyMainContent"`
	Lockdown        bool     `json:"lockdown"`
	MaxAge          int      `json:"maxAge"`
}

type searchResponsePayload struct {
	Success     bool              `json:"success"`
	Data        searchDataPayload `json:"data"`
	CreditsUsed float64           `json:"creditsUsed"`
}

type searchDataPayload struct {
	Web []searchResultPayload `json:"web"`
}

type searchResultPayload struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

type scrapeResponsePayload struct {
	Success     bool              `json:"success"`
	Data        scrapeDataPayload `json:"data"`
	CreditsUsed float64           `json:"creditsUsed"`
}

type scrapeDataPayload struct {
	Markdown string          `json:"markdown"`
	Metadata metadataPayload `json:"metadata"`
}

type metadataPayload struct {
	Title     string `json:"title"`
	SourceURL string `json:"sourceURL"`
	URL       string `json:"url"`
}
