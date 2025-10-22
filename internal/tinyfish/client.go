package tinyfish

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

const (
	MAX_RESPONSE_BYTES = 8 * 1024 * 1024
	PAGE_SIZE          = 10
	MAX_PAGE           = 10
)

// Client calls TinyFish Search and Fetch using isolated provider HTTP clients.
type Client struct {
	searchEndpoint *url.URL
	fetchEndpoint  *url.URL
	searchHTTP     *http.Client
	fetchHTTP      *http.Client
	config         config.TinyFishConfig
	logger         *zap.Logger
}

// New creates a TinyFish client for the production API endpoints.
func New(settings config.TinyFishConfig, searchHTTP, fetchHTTP *http.Client, logger *zap.Logger) (*Client, error) {
	return NewAtURLs(DEFAULT_SEARCH_ENDPOINT, DEFAULT_FETCH_ENDPOINT, settings, searchHTTP, fetchHTTP, logger)
}

// NewAtURLs creates a TinyFish client at supplied endpoints, primarily for tests.
func NewAtURLs(rawSearchEndpoint, rawFetchEndpoint string, settings config.TinyFishConfig, searchHTTP, fetchHTTP *http.Client, logger *zap.Logger) (*Client, error) {
	searchEndpoint, err := parseEndpoint(rawSearchEndpoint, "TinyFish Search")
	if err != nil {
		return nil, err
	}
	fetchEndpoint, err := parseEndpoint(rawFetchEndpoint, "TinyFish Fetch")
	if err != nil {
		return nil, err
	}
	if searchHTTP == nil || fetchHTTP == nil {
		return nil, fmt.Errorf("TinyFish HTTP clients are required")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Client{searchEndpoint: searchEndpoint, fetchEndpoint: fetchEndpoint, searchHTTP: searchHTTP, fetchHTTP: fetchHTTP, config: settings, logger: logger}, nil
}

// Search retrieves TinyFish result pages until it has enough normalized results.
func (c *Client) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	results := make([]SearchResult, 0, request.Limit)
	for page := 0; page <= MAX_PAGE && len(results) < request.Limit; page++ {
		upstream, err := c.searchPage(ctx, request, page)
		if err != nil {
			return SearchResponse{}, err
		}
		for _, result := range upstream.Results {
			if len(results) == request.Limit {
				break
			}
			publishedAt, err := parsePublishedAt(result.Date)
			if err != nil {
				return SearchResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Cause: err}
			}
			results = append(results, SearchResult{Rank: len(results) + 1, URL: result.URL, Title: result.Title, Snippet: result.Snippet, PublishedAt: publishedAt})
		}
		if len(upstream.Results) < PAGE_SIZE {
			break
		}
	}
	return SearchResponse{Results: results}, nil
}

// Fetch retrieves Markdown content for one public URL.
func (c *Client) Fetch(ctx context.Context, request ContentRequest) (ContentResponse, error) {
	payload := fetchRequestPayload{
		URLs:            []string{request.URL},
		Format:          c.config.Fetch.Format,
		Links:           false,
		ImageLinks:      false,
		PerURLTimeoutMS: int(c.config.Fetch.Timeout.Std().Milliseconds()),
	}
	if request.ForceRefresh {
		payload.TTL = pointer(0)
	} else if c.config.Fetch.UseGatewayTTL {
		payload.TTL = pointer(int(request.DocumentTTL.Seconds()))
	}

	var upstream fetchResponsePayload
	if err := c.callJSON(ctx, c.fetchHTTP, http.MethodPost, c.fetchEndpoint, payload, &upstream); err != nil {
		return ContentResponse{}, err
	}
	if len(upstream.Results) > 0 {
		result := upstream.Results[0]
		if strings.TrimSpace(result.Text) == "" {
			return ContentResponse{}, &provider.Failure{Reason: provider.REASON_UNAVAILABLE, Cause: fmt.Errorf("TinyFish returned empty content")}
		}
		publishedAt, err := parsePublishedAt(result.PublishedDate)
		if err != nil {
			return ContentResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Cause: err}
		}
		resultURL := result.FinalURL
		if resultURL == "" {
			resultURL = result.URL
		}
		if resultURL == "" {
			resultURL = request.URL
		}
		return ContentResponse{URL: resultURL, Title: result.Title, Content: result.Text, PublishedAt: publishedAt}, nil
	}
	for _, upstreamError := range upstream.Errors {
		if upstreamError.URL == request.URL {
			return ContentResponse{}, c.classifyFetchError(upstreamError)
		}
	}
	return ContentResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("TinyFish response has no result or error for URL")}
}

func (c *Client) searchPage(ctx context.Context, request SearchRequest, page int) (searchResponsePayload, error) {
	endpoint := *c.searchEndpoint
	values := endpoint.Query()
	values.Set("query", request.Query)
	if c.config.Search.Location != "" {
		values.Set("location", c.config.Search.Location)
	}
	if c.config.Search.Language != "" {
		values.Set("language", c.config.Search.Language)
	}
	values.Set("domain_type", c.config.Search.DomainType)
	values.Set("page", strconv.Itoa(page))
	if len(request.IncludeDomains) > 0 {
		values.Set("include_domains", strings.Join(request.IncludeDomains, ","))
	}
	if len(request.ExcludeDomains) > 0 {
		values.Set("exclude_domains", strings.Join(request.ExcludeDomains, ","))
	}
	if request.PublishedAfter != nil {
		values.Set("after_date", request.PublishedAfter.UTC().Format(time.DateOnly))
	}
	if request.PublishedBefore != nil {
		values.Set("before_date", request.PublishedBefore.UTC().Format(time.DateOnly))
	}
	endpoint.RawQuery = values.Encode()

	var upstream searchResponsePayload
	if err := c.callJSON(ctx, c.searchHTTP, http.MethodGet, &endpoint, nil, &upstream); err != nil {
		return searchResponsePayload{}, err
	}
	return upstream, nil
}

func (c *Client) callJSON(ctx context.Context, client *http.Client, method string, endpoint *url.URL, payload, destination any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal TinyFish request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return fmt.Errorf("create TinyFish request: %w", err)
	}
	request.Header.Set("X-API-Key", c.config.APIKey)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := client.Do(request)
	if err != nil {
		return classifyTransportError(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, MAX_RESPONSE_BYTES+1))
	if err != nil {
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("read TinyFish response: %w", err)}
	}
	if len(responseBody) > MAX_RESPONSE_BYTES {
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("TinyFish response exceeds size limit")}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return c.classifyHTTPError(response.StatusCode)
	}
	if err := json.Unmarshal(responseBody, destination); err != nil {
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("decode TinyFish response: %w", err)}
	}
	c.logger.Debug("TinyFish upstream response", zap.Int("upstream_status", response.StatusCode))
	return nil
}

func (c *Client) classifyHTTPError(status int) error {
	c.logger.Warn("TinyFish upstream request failed", zap.Int("upstream_status", status))
	failure := &provider.Failure{Cause: fmt.Errorf("TinyFish returned HTTP %d", status)}
	switch status {
	case http.StatusUnauthorized:
		failure.Reason = provider.REASON_UNAUTHORIZED
	case http.StatusPaymentRequired:
		failure.Reason = provider.REASON_QUOTA
	case http.StatusForbidden:
		failure.Reason = provider.REASON_FORBIDDEN
	case http.StatusNotFound:
		failure.Reason = provider.REASON_MISCONFIGURED
	case http.StatusTooManyRequests:
		failure.Reason = provider.REASON_RATE_LIMITED
		failure.Retryable = true
	case http.StatusInternalServerError, http.StatusServiceUnavailable:
		failure.Reason = provider.REASON_TEMPORARY
		failure.Retryable = true
	default:
		failure.Reason = provider.REASON_TEMPORARY
	}
	return failure
}

func (c *Client) classifyFetchError(upstreamError fetchErrorPayload) error {
	c.logger.Warn("TinyFish Fetch URL failed", zap.String("error_category", upstreamError.Error))
	switch upstreamError.Error {
	case "page_not_found":
		return &provider.Failure{Reason: provider.REASON_UNAVAILABLE, Terminal: true, Cause: fmt.Errorf("TinyFish page not found")}
	case "target_unreachable", "timeout", "bot_blocked", "proxy_error":
		return &provider.Failure{Reason: provider.REASON_UNAVAILABLE, Cause: fmt.Errorf("TinyFish fetch error: %s", upstreamError.Error)}
	default:
		return &provider.Failure{Reason: provider.REASON_UNAVAILABLE, Cause: fmt.Errorf("TinyFish fetch error: %s", upstreamError.Error)}
	}
}

func parseEndpoint(rawEndpoint, name string) (*url.URL, error) {
	endpoint, err := url.Parse(rawEndpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("parse %s endpoint", name)
	}
	return endpoint, nil
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
	return nil, fmt.Errorf("parse TinyFish publication date")
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

func pointer[T any](value T) *T { return &value }

type fetchRequestPayload struct {
	URLs            []string `json:"urls"`
	Format          string   `json:"format"`
	Links           bool     `json:"links"`
	ImageLinks      bool     `json:"image_links"`
	TTL             *int     `json:"ttl,omitempty"`
	PerURLTimeoutMS int      `json:"per_url_timeout_ms"`
}

type searchResponsePayload struct {
	Results []searchResultPayload `json:"results"`
}

type searchResultPayload struct {
	Position int    `json:"position"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Snippet  string `json:"snippet"`
	Date     string `json:"date"`
}

type fetchResponsePayload struct {
	Results []fetchResultPayload `json:"results"`
	Errors  []fetchErrorPayload  `json:"errors"`
}

type fetchResultPayload struct {
	URL           string `json:"url"`
	FinalURL      string `json:"final_url"`
	Title         string `json:"title"`
	PublishedDate string `json:"published_date"`
	Text          string `json:"text"`
}

type fetchErrorPayload struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}
