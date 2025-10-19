package markdownnew

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
	"unicode/utf8"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"go.uber.org/zap"
)

const MAX_RESPONSE_BYTES = 8 * 1024 * 1024

// Client calls markdown.new using an isolated provider HTTP client.
type Client struct {
	baseURL *url.URL
	http    *http.Client
	config  config.MarkdownNewConfig
	logger  *zap.Logger
}

// New creates a markdown.new client for the configured API endpoint.
func New(settings config.MarkdownNewConfig, client *http.Client, logger *zap.Logger) (*Client, error) {
	return NewAtURL(settings.BaseURL, settings, client, logger)
}

// NewAtURL creates a markdown.new client at a supplied endpoint, primarily for tests.
func NewAtURL(rawBaseURL string, settings config.MarkdownNewConfig, client *http.Client, logger *zap.Logger) (*Client, error) {
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("parse markdown.new base URL")
	}
	if client == nil {
		return nil, fmt.Errorf("markdown.new HTTP client is required")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Client{baseURL: baseURL, http: client, config: settings, logger: logger}, nil
}

// Fetch retrieves Markdown for one public URL.
func (c *Client) Fetch(ctx context.Context, request ContentRequest) (ContentResponse, error) {
	payload := requestPayload{
		URL:          request.URL,
		Method:       c.config.Fetch.Method,
		RetainImages: c.config.Fetch.RetainImages,
	}
	body, err := jsonBody(payload)
	if err != nil {
		return ContentResponse{}, err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL.String(), bytes.NewReader(body))
	if err != nil {
		return ContentResponse{}, fmt.Errorf("create markdown.new request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/markdown, text/plain;q=0.9, */*;q=0.1")

	response, err := c.http.Do(httpRequest)
	if err != nil {
		return ContentResponse{}, classifyTransportError(err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, MAX_RESPONSE_BYTES+1))
	if err != nil {
		return ContentResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("read markdown.new response: %w", err)}
	}
	if len(responseBody) > MAX_RESPONSE_BYTES {
		return ContentResponse{}, &provider.Failure{Reason: provider.REASON_TEMPORARY, Retryable: true, Cause: fmt.Errorf("markdown.new response exceeds size limit")}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ContentResponse{}, c.classifyHTTPError(response.StatusCode)
	}
	if strings.TrimSpace(response.Header.Get("x-rate-limit-remaining")) == "0" {
		c.logger.Warn("markdown.new quota exhausted", zap.Int("upstream_status", response.StatusCode))
		return ContentResponse{}, &provider.Failure{
			Reason:    provider.REASON_RATE_LIMITED,
			Retryable: true,
			Cooldown:  c.config.Fetch.RateLimitCooldown.Std(),
		}
	}

	content := string(responseBody)
	sourceMediaType := mediaType(response.Header.Get("Content-Type"))
	if (sourceMediaType != nil && strings.EqualFold(*sourceMediaType, "application/json")) || unusable(content, c.config.Fetch.MinContentChars) {
		return ContentResponse{}, &provider.Failure{Reason: provider.REASON_UNAVAILABLE, Cause: fmt.Errorf("markdown.new returned unusable content")}
	}
	c.logger.Debug("markdown.new upstream response", zap.Int("upstream_status", response.StatusCode))
	return ContentResponse{Content: content, SourceMediaType: sourceMediaType}, nil
}

func jsonBody(payload requestPayload) ([]byte, error) {
	return json.Marshal(payload)
}

func (c *Client) classifyHTTPError(status int) error {
	c.logger.Warn("markdown.new upstream request failed", zap.Int("upstream_status", status))
	failure := &provider.Failure{Cause: fmt.Errorf("markdown.new returned HTTP %d", status)}
	switch status {
	case http.StatusUnauthorized:
		failure.Reason = provider.REASON_UNAUTHORIZED
	case http.StatusForbidden:
		failure.Reason = provider.REASON_FORBIDDEN
	case http.StatusTooManyRequests:
		failure.Reason = provider.REASON_RATE_LIMITED
		failure.Retryable = true
		failure.Cooldown = c.config.Fetch.RateLimitCooldown.Std()
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		failure.Reason = provider.REASON_TEMPORARY
		failure.Retryable = true
	default:
		failure.Reason = provider.REASON_UNAVAILABLE
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

func unusable(content string, minContentChars int) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
		return true
	}
	if utf8.RuneCountInString(trimmed) >= minContentChars {
		return false
	}
	for _, marker := range []string{
		"access denied",
		"content blocked",
		"error occurred",
		"forbidden",
		"rate limit exceeded",
		"too many requests",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func mediaType(value string) *string {
	mediaType, _, _ := strings.Cut(value, ";")
	mediaType = strings.TrimSpace(mediaType)
	if mediaType == "" {
		return nil
	}
	return &mediaType
}

type requestPayload struct {
	URL          string `json:"url"`
	Method       string `json:"method"`
	RetainImages bool   `json:"retain_images"`
}
