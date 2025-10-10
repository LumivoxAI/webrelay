package exa

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

// Client calls Exa Search and Contents using an isolated provider HTTP client.
type Client struct {
	baseURL *url.URL
	http    *http.Client
	config  config.ExaConfig
	logger  *zap.Logger
}

// New creates an Exa client for the production API endpoint.
func New(settings config.ExaConfig, client *http.Client, logger *zap.Logger) (*Client, error) {
	return NewAtURL(DEFAULT_BASE_URL, settings, client, logger)
}

// NewAtURL creates an Exa client at a supplied base URL, primarily for tests.
func NewAtURL(rawBaseURL string, settings config.ExaConfig, client *http.Client, logger *zap.Logger) (*Client, error) {
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("parse Exa base URL")
	}
	if client == nil {
		return nil, fmt.Errorf("Exa HTTP client is required")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Client{baseURL: baseURL, http: client, config: settings, logger: logger}, nil
}

// Search retrieves and normalizes Exa search results.
func (c *Client) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	payload := searchPayload{
		Query:              request.Query,
		Type:               c.config.SearchType,
		NumResults:         request.Limit,
		IncludeDomains:     request.IncludeDomains,
		ExcludeDomains:     request.ExcludeDomains,
		StartPublishedDate: formatTime(request.PublishedAfter),
		EndPublishedDate:   formatTime(request.PublishedBefore),
	}
	if c.config.SearchWithText || c.config.SearchWithHighlights {
		payload.Contents = &searchContentsPayload{}
		if c.config.SearchWithText {
			payload.Contents.Text = &textPayload{MaxCharacters: c.config.MaxContentCharacters}
		}
		if c.config.SearchWithHighlights {
			payload.Contents.Highlights = true
		}
	}

	var upstream searchResponsePayload
	if err := c.call(ctx, http.MethodPost, "search", payload, &upstream); err != nil {
		return SearchResponse{}, err
	}

	results := make([]SearchResult, 0, len(upstream.Results))
	for index, result := range upstream.Results {
		publishedAt, err := parsePublishedAt(result.PublishedDate)
		if err != nil {
			return SearchResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Cause: err}
		}
		results = append(results, SearchResult{
			Rank:            index + 1,
			URL:             result.URL,
			Title:           result.Title,
			Snippet:         snippet(result.Highlights, result.Text),
			PublishedAt:     publishedAt,
			EmbeddedContent: result.Text,
		})
	}
	return SearchResponse{Results: results}, nil
}

// Contents retrieves and normalizes one Exa document.
func (c *Client) Contents(ctx context.Context, request ContentRequest) (ContentResponse, error) {
	payload := contentsRequestPayload{
		IDs:         []string{request.URL},
		Text:        textPayload{MaxCharacters: c.config.MaxContentCharacters},
		MaxAgeHours: c.config.MaxAgeHours,
	}
	var upstream contentsResponsePayload
	if err := c.call(ctx, http.MethodPost, "contents", payload, &upstream); err != nil {
		return ContentResponse{}, err
	}
	if err := verifyStatus(upstream.Statuses, request.URL); err != nil {
		return ContentResponse{}, err
	}
	if len(upstream.Results) == 0 {
		return ContentResponse{}, &provider.Failure{Reason: provider.REASON_UNAVAILABLE}
	}

	result := upstream.Results[0]
	return ContentResponse{
		URL:             result.URL,
		Title:           result.Title,
		Content:         result.Text,
		SourceMediaType: result.SourceMediaType,
	}, nil
}

func (c *Client) call(ctx context.Context, method, path string, payload, destination any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal Exa request: %w", err)
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Exa request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-api-key", c.config.APIKey)

	response, err := c.http.Do(request)
	if err != nil {
		return classifyTransportError(err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, MAX_RESPONSE_BYTES+1))
	if err != nil {
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("read Exa response: %w", err)}
	}
	if len(responseBody) > MAX_RESPONSE_BYTES {
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("Exa response exceeds size limit")}
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return c.classifyHTTPError(response.StatusCode, responseBody)
	}
	if err := json.Unmarshal(responseBody, destination); err != nil {
		return &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("decode Exa response: %w", err)}
	}
	c.logger.Debug("Exa upstream response", zap.Int("upstream_status", response.StatusCode))
	return nil
}

func (c *Client) classifyHTTPError(status int, body []byte) error {
	var upstream exaErrorPayload
	_ = json.Unmarshal(body, &upstream)
	fields := []zap.Field{zap.Int("upstream_status", status)}
	if upstream.Tag != "" {
		fields = append(fields, zap.String("exa_tag", upstream.Tag))
	}
	if upstream.RequestID != "" {
		fields = append(fields, zap.String("upstream_request_id", upstream.RequestID))
	}
	c.logger.Warn("Exa upstream request failed", fields...)

	failure := &provider.Failure{Cause: fmt.Errorf("Exa returned HTTP %d", status)}
	switch status {
	case http.StatusUnauthorized:
		failure.Reason = provider.REASON_UNAUTHORIZED
	case http.StatusPaymentRequired:
		failure.Reason = provider.REASON_QUOTA
	case http.StatusForbidden:
		failure.Reason = provider.REASON_FORBIDDEN
	case http.StatusTooManyRequests:
		failure.Reason = provider.REASON_RATE_LIMITED
		failure.Retryable = true
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		failure.Reason = provider.REASON_TEMPORARY
		failure.Retryable = true
	default:
		failure.Reason = provider.REASON_TEMPORARY
	}
	return failure
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

func verifyStatus(statuses []contentStatusPayload, requestedURL string) error {
	for _, status := range statuses {
		if status.ID == requestedURL && !strings.EqualFold(status.Status, "success") {
			return &provider.Failure{Reason: provider.REASON_UNAVAILABLE, Cause: fmt.Errorf("Exa content status: %s", status.Status)}
		}
	}
	return nil
}

func snippet(highlights []string, text string) string {
	if len(highlights) > 0 {
		return strings.Join(highlights, "\n")
	}
	return text
}

func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func parsePublishedAt(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("parse Exa publication date: %w", err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

type searchPayload struct {
	Query              string                 `json:"query"`
	Type               string                 `json:"type"`
	NumResults         int                    `json:"numResults"`
	IncludeDomains     []string               `json:"includeDomains,omitempty"`
	ExcludeDomains     []string               `json:"excludeDomains,omitempty"`
	StartPublishedDate string                 `json:"startPublishedDate,omitempty"`
	EndPublishedDate   string                 `json:"endPublishedDate,omitempty"`
	Contents           *searchContentsPayload `json:"contents,omitempty"`
}

type searchContentsPayload struct {
	Text       *textPayload `json:"text,omitempty"`
	Highlights bool         `json:"highlights,omitempty"`
}

type textPayload struct {
	MaxCharacters int `json:"maxCharacters"`
}

type contentsRequestPayload struct {
	IDs         []string    `json:"ids"`
	Text        textPayload `json:"text"`
	MaxAgeHours *int        `json:"maxAgeHours,omitempty"`
}

type searchResponsePayload struct {
	Results []searchResultPayload `json:"results"`
}

type searchResultPayload struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Text          string   `json:"text"`
	Highlights    []string `json:"highlights"`
	PublishedDate string   `json:"publishedDate"`
}

type contentsResponsePayload struct {
	Results  []contentResultPayload `json:"results"`
	Statuses []contentStatusPayload `json:"statuses"`
}

type contentResultPayload struct {
	URL             string  `json:"url"`
	Title           string  `json:"title"`
	Text            string  `json:"text"`
	SourceMediaType *string `json:"sourceMediaType"`
}

type contentStatusPayload struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type exaErrorPayload struct {
	RequestID string `json:"requestId"`
	Error     string `json:"error"`
	Tag       string `json:"tag"`
}
