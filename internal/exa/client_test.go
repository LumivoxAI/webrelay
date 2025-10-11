package exa

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

type ClientSuite struct {
	suite.Suite
	settings config.ExaConfig
}

func (s *ClientSuite) SetupTest() {
	s.settings = config.DefaultExaConfig()
	s.settings.APIKey = "exa-secret"
}

func (s *ClientSuite) client(server *httptest.Server) *Client {
	client, err := NewAtURL(server.URL, s.settings, server.Client(), zap.NewNop())
	s.Require().NoError(err)
	return client
}

func (s *ClientSuite) TestSearchSendsExpectedPayloadAndSeparatesEmbeddedContent() {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal(http.MethodPost, request.Method)
		s.Equal("/search", request.URL.Path)
		s.Equal("exa-secret", request.Header.Get("x-api-key"))
		s.Equal("application/json", request.Header.Get("Content-Type"))
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		s.Require().NoError(json.Unmarshal(body, &received))
		_, _ = writer.Write([]byte(`{
			"results": [{
				"url": "https://example.com/article",
				"title": "Article",
				"text": "full embedded content",
				"highlights": ["first highlight", "second highlight"],
				"publishedDate": "2026-08-01T12:00:00Z"
			}]
		}`))
	}))
	defer server.Close()

	after := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, time.August, 2, 0, 0, 0, 0, time.UTC)
	response, err := s.client(server).Search(context.Background(), SearchRequest{
		Query:           "Go HTTP client",
		Limit:           10,
		IncludeDomains:  []string{"go.dev"},
		ExcludeDomains:  []string{"example.net"},
		PublishedAfter:  &after,
		PublishedBefore: &before,
	})

	s.Require().NoError(err)
	s.Equal("Go HTTP client", received["query"])
	s.Equal("auto", received["type"])
	s.Equal(float64(10), received["numResults"])
	s.Equal("2026-01-01T00:00:00Z", received["startPublishedDate"])
	s.Equal("2026-08-02T00:00:00Z", received["endPublishedDate"])
	contents := received["contents"].(map[string]any)
	s.Equal(float64(s.settings.MaxContentCharacters), contents["text"].(map[string]any)["maxCharacters"])
	s.True(contents["highlights"].(bool))
	s.Require().Len(response.Results, 1)
	result := response.Results[0]
	s.Equal(1, result.Rank)
	s.Equal("first highlight\nsecond highlight", result.Snippet)
	s.Equal("full embedded content", result.EmbeddedContent)
	s.Equal("2026-08-01T12:00:00Z", result.PublishedAt.Format(time.RFC3339))
}

func (s *ClientSuite) TestSearchUsesTextAsSnippetWithoutHighlights() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"results":[{"url":"https://example.com","title":"Example","text":"text fallback"}]}`))
	}))
	defer server.Close()

	response, err := s.client(server).Search(context.Background(), SearchRequest{Query: "example", Limit: 1})

	s.Require().NoError(err)
	s.Require().Len(response.Results, 1)
	s.Equal("text fallback", response.Results[0].Snippet)
	s.Nil(response.Results[0].PublishedAt)
}

func (s *ClientSuite) TestContentsSendsTopLevelTextAndAcceptsSuccessStatus() {
	maxAgeHours := 24
	s.settings.MaxAgeHours = &maxAgeHours
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal("/contents", request.URL.Path)
		body, err := io.ReadAll(request.Body)
		s.Require().NoError(err)
		s.Require().NoError(json.Unmarshal(body, &received))
		_, _ = writer.Write([]byte(`{
			"results": [{"url":"https://example.com","title":"Example","text":"# Content","sourceMediaType":"text/markdown"}],
			"statuses": [{"id":"https://example.com","status":"success"}]
		}`))
	}))
	defer server.Close()

	response, err := s.client(server).Contents(context.Background(), ContentRequest{URL: "https://example.com"})

	s.Require().NoError(err)
	s.Equal([]any{"https://example.com"}, received["ids"])
	s.Equal(float64(s.settings.MaxContentCharacters), received["text"].(map[string]any)["maxCharacters"])
	s.Equal(float64(24), received["maxAgeHours"])
	s.NotContains(received, "contents")
	s.Equal("# Content", response.Content)
	s.Require().NotNil(response.SourceMediaType)
	s.Equal("text/markdown", *response.SourceMediaType)
}

func (s *ClientSuite) TestContentsRejectsPerURLStatusError() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{
			"results": [],
			"statuses": [{"id":"https://example.com","status":"CRAWL_TIMEOUT"}]
		}`))
	}))
	defer server.Close()

	_, err := s.client(server).Contents(context.Background(), ContentRequest{URL: "https://example.com"})

	var failure *provider.Failure
	s.Require().True(errors.As(err, &failure))
	s.Equal(provider.REASON_UNAVAILABLE, failure.Reason)
	s.False(failure.Retryable)
}

func (s *ClientSuite) TestHTTPFailuresAreClassified() {
	tests := []struct {
		status    int
		reason    provider.Reason
		retryable bool
	}{
		{status: http.StatusUnauthorized, reason: provider.REASON_UNAUTHORIZED},
		{status: http.StatusPaymentRequired, reason: provider.REASON_QUOTA},
		{status: http.StatusTooManyRequests, reason: provider.REASON_RATE_LIMITED, retryable: true},
		{status: http.StatusServiceUnavailable, reason: provider.REASON_TEMPORARY, retryable: true},
	}
	for _, test := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(test.status)
			_, _ = writer.Write([]byte(`{"requestId":"req_123","tag":"UPSTREAM_ERROR","error":"not safe to expose"}`))
		}))

		_, err := s.client(server).Search(context.Background(), SearchRequest{Query: "example", Limit: 1})
		server.Close()

		var failure *provider.Failure
		s.Require().True(errors.As(err, &failure), "status %d", test.status)
		s.Equal(test.reason, failure.Reason, "status %d", test.status)
		s.Equal(test.retryable, failure.Retryable, "status %d", test.status)
	}
}

func (s *ClientSuite) TestTimeoutIsRetryable() {
	client, err := NewAtURL(DEFAULT_BASE_URL, s.settings, &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		}),
	}, zap.NewNop())
	s.Require().NoError(err)

	_, err = client.Search(context.Background(), SearchRequest{Query: "example", Limit: 1})

	var failure *provider.Failure
	s.Require().True(errors.As(err, &failure))
	s.Equal(provider.REASON_TIMEOUT, failure.Reason)
	s.True(failure.Retryable)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}
