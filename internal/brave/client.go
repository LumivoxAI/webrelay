package brave

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"go.uber.org/zap"
)

const (
	MAX_RESPONSE_BYTES = 8 * 1024 * 1024
	MAX_QUERY_CHARS    = 400
	MAX_QUERY_WORDS    = 50
)

// Client calls Brave Web Search using an isolated provider HTTP client.
type Client struct {
	endpoint *url.URL
	http     *http.Client
	config   config.BraveConfig
	logger   *zap.Logger
	now      func() time.Time
}

// New creates a Brave client for the production API endpoint.
func New(settings config.BraveConfig, client *http.Client, logger *zap.Logger) (*Client, error) {
	return NewAtURL(DEFAULT_ENDPOINT, settings, client, logger)
}

// NewAtURL creates a Brave client at a supplied endpoint, primarily for tests.
func NewAtURL(rawEndpoint string, settings config.BraveConfig, client *http.Client, logger *zap.Logger) (*Client, error) {
	endpoint, err := url.Parse(rawEndpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("parse Brave endpoint")
	}
	if client == nil {
		return nil, fmt.Errorf("Brave HTTP client is required")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Client{endpoint: endpoint, http: client, config: settings, logger: logger, now: time.Now}, nil
}

// Search retrieves and normalizes Brave Web Search results.
func (c *Client) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	query, err := queryWithDomainFilters(request.Query, request.IncludeDomains, request.ExcludeDomains)
	if err != nil {
		return SearchResponse{}, err
	}

	endpoint := *c.endpoint
	values := endpoint.Query()
	values.Set("q", query)
	values.Set("count", strconv.Itoa(request.Limit))
	values.Set("country", c.config.Country)
	values.Set("search_lang", c.config.SearchLang)
	values.Set("ui_lang", c.config.UILang)
	values.Set("safesearch", c.config.Safesearch)
	values.Set("spellcheck", strconv.FormatBool(c.config.Spellcheck))
	values.Set("text_decorations", "false")
	values.Set("result_filter", "web")
	if freshness, warning := c.freshness(request.PublishedAfter, request.PublishedBefore); freshness != "" {
		values.Set("freshness", freshness)
	} else if warning {
		c.logger.Warn("Brave freshness filter skipped because only published_before was supplied")
	}
	endpoint.RawQuery = values.Encode()

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return SearchResponse{}, fmt.Errorf("create Brave request: %w", err)
	}
	httpRequest.Header.Set("X-Subscription-Token", c.config.APIKey)
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Accept-Encoding", "gzip")

	response, err := c.http.Do(httpRequest)
	if err != nil {
		return SearchResponse{}, classifyTransportError(err)
	}
	defer response.Body.Close()

	c.logRateLimitHeaders(response.Header)
	responseBody, err := readResponseBody(response)
	if err != nil {
		return SearchResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("read Brave response: %w", err)}
	}
	if len(responseBody) > MAX_RESPONSE_BYTES {
		return SearchResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("Brave response exceeds size limit")}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return SearchResponse{}, c.classifyHTTPError(response.StatusCode, response.Header)
	}

	var upstream responsePayload
	if err := json.Unmarshal(responseBody, &upstream); err != nil {
		return SearchResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("decode Brave response: %w", err)}
	}
	results := make([]SearchResult, 0, len(upstream.Web.Results))
	for index, result := range upstream.Web.Results {
		publishedAt, err := parsePublishedAt(result.PageAge)
		if err != nil {
			return SearchResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Cause: err}
		}
		results = append(results, SearchResult{
			Rank:        index + 1,
			URL:         result.URL,
			Title:       result.Title,
			Snippet:     result.Description,
			PublishedAt: publishedAt,
		})
	}
	c.logger.Debug("Brave upstream response", zap.Int("upstream_status", response.StatusCode))
	return SearchResponse{Results: results}, nil
}

func readResponseBody(response *http.Response) ([]byte, error) {
	reader := io.Reader(response.Body)
	if strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		gzipReader, err := gzip.NewReader(response.Body)
		if err != nil {
			return nil, fmt.Errorf("create Brave gzip reader: %w", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	return io.ReadAll(io.LimitReader(reader, MAX_RESPONSE_BYTES+1))
}

func (c *Client) logRateLimitHeaders(headers http.Header) {
	fields := make([]zap.Field, 0, 4)
	for _, rateLimitHeader := range []struct {
		header string
		field  string
	}{
		{header: "X-RateLimit-Limit", field: "rate_limit_limit"},
		{header: "X-RateLimit-Policy", field: "rate_limit_policy"},
		{header: "X-RateLimit-Remaining", field: "rate_limit_remaining"},
		{header: "X-RateLimit-Reset", field: "rate_limit_reset"},
	} {
		if value := headers.Get(rateLimitHeader.header); value != "" {
			fields = append(fields, zap.String(rateLimitHeader.field, value))
		}
	}
	if len(fields) > 0 {
		c.logger.Debug("Brave rate limit headers", fields...)
	}
}

func queryWithDomainFilters(query string, includeDomains, excludeDomains []string) (string, error) {
	filters := make([]string, 0, len(includeDomains)+len(excludeDomains)+1)
	filters = append(filters, query)
	for _, domain := range includeDomains {
		filters = append(filters, "site:"+domain)
	}
	for _, domain := range excludeDomains {
		filters = append(filters, "-site:"+domain)
	}
	result := strings.Join(filters, " ")
	if utf8.RuneCountInString(result) > MAX_QUERY_CHARS {
		return "", &provider.Failure{Reason: provider.REASON_TEMPORARY, Cause: fmt.Errorf("Brave query exceeds character limit")}
	}
	if len(strings.Fields(result)) > MAX_QUERY_WORDS {
		return "", &provider.Failure{Reason: provider.REASON_TEMPORARY, Cause: fmt.Errorf("Brave query exceeds word limit")}
	}
	return result, nil
}

func (c *Client) freshness(after, before *time.Time) (string, bool) {
	if after == nil {
		return "", before != nil
	}
	end := c.now().UTC()
	if before != nil {
		end = before.UTC()
	}
	return after.UTC().Format(time.DateOnly) + "to" + end.Format(time.DateOnly), false
}

func (c *Client) classifyHTTPError(status int, headers http.Header) error {
	c.logger.Warn("Brave upstream request failed", zap.Int("upstream_status", status))
	failure := &provider.Failure{Cause: fmt.Errorf("Brave returned HTTP %d", status)}
	switch status {
	case http.StatusUnauthorized:
		failure.Reason = provider.REASON_UNAUTHORIZED
	case http.StatusForbidden:
		failure.Reason = provider.REASON_FORBIDDEN
	case http.StatusTooManyRequests:
		failure.Reason = provider.REASON_RATE_LIMITED
		failure.Retryable = true
		failure.Cooldown = cooldown(headers.Get("X-RateLimit-Reset"))
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		failure.Reason = provider.REASON_TEMPORARY
		failure.Retryable = true
	default:
		failure.Reason = provider.REASON_TEMPORARY
	}
	return failure
}

func cooldown(value string) time.Duration {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || seconds <= 0 || math.IsInf(seconds, 0) || math.IsNaN(seconds) {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
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

func parsePublishedAt(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", time.DateOnly} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("parse Brave publication date")
}

type responsePayload struct {
	Web webPayload `json:"web"`
}

type webPayload struct {
	Results []searchResultPayload `json:"results"`
}

type searchResultPayload struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	PageAge     string `json:"page_age"`
}
