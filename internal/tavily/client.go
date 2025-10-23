package tavily

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
	"strings"
	"time"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"go.uber.org/zap"
)

const MAX_RESPONSE_BYTES = 8 * 1024 * 1024

// Client calls Tavily Search and Extract using isolated provider HTTP clients.
type Client struct {
	baseURL     *url.URL
	searchHTTP  *http.Client
	extractHTTP *http.Client
	config      config.TavilyConfig
	metrics     *provider.Metrics
	logger      *zap.Logger
}

// New creates a Tavily client for the production API endpoint.
func New(settings config.TavilyConfig, searchHTTP, extractHTTP *http.Client, metrics *provider.Metrics, logger *zap.Logger) (*Client, error) {
	return NewAtURL(DEFAULT_BASE_URL, settings, searchHTTP, extractHTTP, metrics, logger)
}

// NewAtURL creates a Tavily client at a supplied base URL, primarily for tests.
func NewAtURL(rawBaseURL string, settings config.TavilyConfig, searchHTTP, extractHTTP *http.Client, metrics *provider.Metrics, logger *zap.Logger) (*Client, error) {
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("parse Tavily base URL")
	}
	if searchHTTP == nil || extractHTTP == nil {
		return nil, fmt.Errorf("Tavily HTTP clients are required")
	}
	if metrics == nil {
		metrics = provider.NewMetrics(nil)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Client{baseURL: baseURL, searchHTTP: searchHTTP, extractHTTP: extractHTTP, config: settings, metrics: metrics, logger: logger}, nil
}

// Search retrieves and normalizes Tavily search results without raw content.
func (c *Client) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	payload := searchRequestPayload{
		Query:             request.Query,
		SearchDepth:       c.config.Search.SearchDepth,
		AutoParameters:    c.config.Search.AutoParameters,
		Topic:             c.config.Search.Topic,
		MaxResults:        request.Limit,
		IncludeDomains:    request.IncludeDomains,
		ExcludeDomains:    request.ExcludeDomains,
		StartDate:         formatDate(request.PublishedAfter),
		EndDate:           formatDate(request.PublishedBefore),
		IncludeAnswer:     false,
		IncludeRawContent: c.config.Search.IncludeRawContent,
		IncludeImages:     false,
		IncludeUsage:      c.config.Search.IncludeUsage,
	}
	var upstream searchResponsePayload
	if err := c.call(ctx, c.searchHTTP, "search", payload, &upstream); err != nil {
		return SearchResponse{}, err
	}
	c.recordCredits(provider.SEARCH, upstream.Usage.Credits)

	results := make([]SearchResult, 0, len(upstream.Results))
	for index, result := range upstream.Results {
		publishedAt, err := parsePublishedAt(result.PublishedDate)
		if err != nil {
			return SearchResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Cause: err}
		}
		results = append(results, SearchResult{Rank: index + 1, URL: result.URL, Title: result.Title, Snippet: result.Content, PublishedAt: publishedAt})
	}
	return SearchResponse{Results: results}, nil
}

// Extract retrieves Markdown content for one public URL.
func (c *Client) Extract(ctx context.Context, request ContentRequest) (ContentResponse, error) {
	payload := extractRequestPayload{
		URLs:          []string{request.URL},
		ExtractDepth:  c.config.Extract.ExtractDepth,
		Format:        c.config.Extract.Format,
		IncludeImages: false,
		Timeout:       c.config.Extract.Timeout.Std().Seconds(),
		IncludeUsage:  c.config.Extract.IncludeUsage,
	}
	var upstream extractResponsePayload
	if err := c.call(ctx, c.extractHTTP, "extract", payload, &upstream); err != nil {
		return ContentResponse{}, err
	}
	c.recordCredits(provider.EXTRACT, upstream.Usage.Credits)
	for _, result := range upstream.Results {
		if result.URL == request.URL {
			if strings.TrimSpace(result.RawContent) == "" {
				return ContentResponse{}, &provider.Failure{Reason: provider.REASON_UNAVAILABLE, Cause: fmt.Errorf("Tavily returned empty content")}
			}
			return ContentResponse{URL: result.URL, Content: result.RawContent}, nil
		}
	}
	for _, result := range upstream.FailedResults {
		if result.URL == request.URL {
			return ContentResponse{}, &provider.Failure{Reason: provider.REASON_UNAVAILABLE, Cause: fmt.Errorf("Tavily extract failed")}
		}
	}
	return ContentResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("Tavily response has no result or failure for URL")}
}

func (c *Client) call(ctx context.Context, client *http.Client, path string, payload, destination any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal Tavily request: %w", err)
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Tavily request: %w", err)
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
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("read Tavily response: %w", err)}
	}
	if len(responseBody) > MAX_RESPONSE_BYTES {
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("Tavily response exceeds size limit")}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return c.classifyHTTPError(response.StatusCode, responseBody)
	}
	if err := json.Unmarshal(responseBody, destination); err != nil {
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("decode Tavily response: %w", err)}
	}
	c.logger.Debug("Tavily upstream response", zap.Int("upstream_status", response.StatusCode))
	return nil
}

func (c *Client) classifyHTTPError(status int, body []byte) error {
	var upstream errorResponsePayload
	_ = json.Unmarshal(body, &upstream)
	fields := []zap.Field{zap.Int("upstream_status", status)}
	if upstream.RequestID != "" {
		fields = append(fields, zap.String("upstream_request_id", upstream.RequestID))
	}
	c.logger.Warn("Tavily upstream request failed", fields...)

	failure := &provider.Failure{Cause: fmt.Errorf("Tavily returned HTTP %d", status)}
	switch status {
	case http.StatusUnauthorized:
		failure.Reason = provider.REASON_UNAUTHORIZED
	case http.StatusForbidden:
		failure.Reason = provider.REASON_FORBIDDEN
	case http.StatusTooManyRequests:
		failure.Reason = provider.REASON_RATE_LIMITED
		failure.Retryable = true
	case 432, 433:
		failure.Reason = provider.REASON_QUOTA
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		failure.Reason = provider.REASON_TEMPORARY
		failure.Retryable = true
	default:
		failure.Reason = provider.REASON_TEMPORARY
	}
	return failure
}

func (c *Client) recordCredits(action provider.Action, credits float64) {
	c.metrics.RecordCredits(provider.Key{Provider: provider.TAVILY, Action: action}, credits)
}

func formatDate(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.DateOnly)
}

func parsePublishedAt(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("parse Tavily publication date")
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
	Query             string   `json:"query"`
	SearchDepth       string   `json:"search_depth"`
	AutoParameters    bool     `json:"auto_parameters"`
	Topic             string   `json:"topic"`
	MaxResults        int      `json:"max_results"`
	IncludeDomains    []string `json:"include_domains,omitempty"`
	ExcludeDomains    []string `json:"exclude_domains,omitempty"`
	StartDate         string   `json:"start_date,omitempty"`
	EndDate           string   `json:"end_date,omitempty"`
	IncludeAnswer     bool     `json:"include_answer"`
	IncludeRawContent bool     `json:"include_raw_content"`
	IncludeImages     bool     `json:"include_images"`
	IncludeUsage      bool     `json:"include_usage"`
}

type extractRequestPayload struct {
	URLs          []string `json:"urls"`
	ExtractDepth  string   `json:"extract_depth"`
	Format        string   `json:"format"`
	IncludeImages bool     `json:"include_images"`
	Timeout       float64  `json:"timeout"`
	IncludeUsage  bool     `json:"include_usage"`
}

type usagePayload struct {
	Credits float64 `json:"credits"`
}

type searchResponsePayload struct {
	Results []searchResultPayload `json:"results"`
	Usage   usagePayload          `json:"usage"`
}

type searchResultPayload struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Content       string `json:"content"`
	PublishedDate string `json:"published_date"`
}

type extractResponsePayload struct {
	Results       []extractResultPayload `json:"results"`
	FailedResults []failedResultPayload  `json:"failed_results"`
	Usage         usagePayload           `json:"usage"`
}

type extractResultPayload struct {
	URL        string `json:"url"`
	RawContent string `json:"raw_content"`
}

type failedResultPayload struct {
	URL string `json:"url"`
}

type errorResponsePayload struct {
	RequestID string `json:"request_id"`
}
