package brave

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type ClientSuite struct {
	suite.Suite
	settings config.BraveConfig
}

func (s *ClientSuite) SetupTest() {
	s.settings = config.DefaultBraveConfig()
	s.settings.APIKey = "brave-secret"
}

func (s *ClientSuite) client(server *httptest.Server, logger *zap.Logger) *Client {
	client, err := NewAtURL(server.URL, s.settings, server.Client(), logger)
	s.Require().NoError(err)
	return client
}

func (s *ClientSuite) TestSearchSendsExpectedQueryAndNormalizesResults() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal(http.MethodGet, request.Method)
		s.Equal("brave-secret", request.Header.Get("X-Subscription-Token"))
		s.Equal("application/json", request.Header.Get("Accept"))
		s.Equal("gzip", request.Header.Get("Accept-Encoding"))
		s.Equal("Go HTTP site:go.dev -site:example.com", request.URL.Query().Get("q"))
		s.Equal("10", request.URL.Query().Get("count"))
		s.Equal("RU", request.URL.Query().Get("country"))
		s.Equal("en", request.URL.Query().Get("search_lang"))
		s.Equal("en-US", request.URL.Query().Get("ui_lang"))
		s.Equal("moderate", request.URL.Query().Get("safesearch"))
		s.Equal("true", request.URL.Query().Get("spellcheck"))
		s.Equal("false", request.URL.Query().Get("text_decorations"))
		s.Equal("web", request.URL.Query().Get("result_filter"))
		_, _ = writer.Write([]byte(`{"web":{"results":[{"title":"Article","url":"https://example.com/article","description":"Snippet","page_age":"2026-08-01T12:00:00"}]}}`))
	}))
	defer server.Close()

	response, err := s.client(server, zap.NewNop()).Search(context.Background(), SearchRequest{
		Query:          "Go HTTP",
		Limit:          10,
		IncludeDomains: []string{"go.dev"},
		ExcludeDomains: []string{"example.com"},
	})

	s.Require().NoError(err)
	s.Require().Len(response.Results, 1)
	result := response.Results[0]
	s.Equal(1, result.Rank)
	s.Equal("Article", result.Title)
	s.Equal("https://example.com/article", result.URL)
	s.Equal("Snippet", result.Snippet)
	s.Require().NotNil(result.PublishedAt)
	s.Equal("2026-08-01T12:00:00Z", result.PublishedAt.Format(time.RFC3339))
}

func (s *ClientSuite) TestSearchDecompressesGzipResponse() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		var body bytes.Buffer
		zipWriter := gzip.NewWriter(&body)
		_, err := zipWriter.Write([]byte(`{"web":{"results":[]}}`))
		s.Require().NoError(err)
		s.Require().NoError(zipWriter.Close())
		writer.Header().Set("Content-Encoding", "gzip")
		_, _ = writer.Write(body.Bytes())
	}))
	defer server.Close()

	response, err := s.client(server, zap.NewNop()).Search(context.Background(), SearchRequest{Query: "example", Limit: 1})

	s.Require().NoError(err)
	s.Empty(response.Results)
}

func (s *ClientSuite) TestSearchFormatsFreshness() {
	tests := []struct {
		name     string
		after    *time.Time
		before   *time.Time
		expected string
	}{
		{name: "after only", after: pointerToTime(time.Date(2026, time.January, 2, 12, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))), expected: "2026-01-02to2026-08-09"},
		{name: "both bounds", after: pointerToTime(time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)), before: pointerToTime(time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC)), expected: "2026-01-02to2026-08-03"},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				s.Equal(test.expected, request.URL.Query().Get("freshness"))
				_, _ = writer.Write([]byte(`{"web":{"results":[]}}`))
			}))
			defer server.Close()
			client := s.client(server, zap.NewNop())
			client.now = func() time.Time { return time.Date(2026, time.August, 9, 15, 0, 0, 0, time.UTC) }

			_, err := client.Search(context.Background(), SearchRequest{Query: "example", Limit: 1, PublishedAfter: test.after, PublishedBefore: test.before})

			s.Require().NoError(err)
		})
	}
}

func (s *ClientSuite) TestSearchSkipsBeforeOnlyFreshnessWithWarning() {
	core, logs := observer.New(zap.WarnLevel)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Empty(request.URL.Query().Get("freshness"))
		_, _ = writer.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer server.Close()

	before := time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC)
	_, err := s.client(server, zap.New(core)).Search(context.Background(), SearchRequest{Query: "example", Limit: 1, PublishedBefore: &before})

	s.Require().NoError(err)
	s.Len(logs.FilterMessage("Brave freshness filter skipped because only published_before was supplied").All(), 1)
}

func (s *ClientSuite) TestSearchRejectsDomainFiltersThatExceedBraveLimits() {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		s.Fail("Brave must not receive an over-limit query")
	}))
	defer server.Close()

	_, err := s.client(server, zap.NewNop()).Search(context.Background(), SearchRequest{
		Query:          strings.Repeat("a", MAX_QUERY_CHARS),
		Limit:          1,
		IncludeDomains: []string{"go.dev"},
	})

	var failure *provider.Failure
	s.Require().True(errors.As(err, &failure))
	s.Equal(provider.REASON_TEMPORARY, failure.Reason)
}

func (s *ClientSuite) TestHTTPFailuresAreClassifiedAndReadResetCooldown() {
	tests := []struct {
		status    int
		reason    provider.Reason
		retryable bool
		reset     string
		cooldown  time.Duration
	}{
		{status: http.StatusUnauthorized, reason: provider.REASON_UNAUTHORIZED},
		{status: http.StatusTooManyRequests, reason: provider.REASON_RATE_LIMITED, retryable: true, reset: "3.5", cooldown: 3500 * time.Millisecond},
		{status: http.StatusServiceUnavailable, reason: provider.REASON_TEMPORARY, retryable: true},
	}
	for _, test := range tests {
		s.Run(http.StatusText(test.status), func() {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("X-RateLimit-Reset", test.reset)
				writer.WriteHeader(test.status)
			}))
			defer server.Close()

			_, err := s.client(server, zap.NewNop()).Search(context.Background(), SearchRequest{Query: "example", Limit: 1})

			var failure *provider.Failure
			s.Require().True(errors.As(err, &failure))
			s.Equal(test.reason, failure.Reason)
			s.Equal(test.retryable, failure.Retryable)
			s.Equal(test.cooldown, failure.Cooldown)
		})
	}
}

func (s *ClientSuite) TestTimeoutIsRetryable() {
	client, err := NewAtURL(DEFAULT_ENDPOINT, s.settings, &http.Client{
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

func pointerToTime(value time.Time) *time.Time {
	return &value
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}
