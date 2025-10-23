package tavily

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

type ClientSuite struct {
	suite.Suite
	settings config.TavilyConfig
	metrics  *provider.Metrics
}

func (s *ClientSuite) SetupTest() {
	s.settings = config.DefaultTavilyConfig()
	s.settings.APIKey = "tavily-secret"
	s.metrics = provider.NewMetrics(zap.NewNop())
}

func (s *ClientSuite) client(server *httptest.Server) *Client {
	client, err := NewAtURL(server.URL+"/", s.settings, server.Client(), server.Client(), s.metrics, zap.NewNop())
	s.Require().NoError(err)
	return client
}

func (s *ClientSuite) TestSearchSendsBasicConfigurationAndNormalizesResults() {
	var received searchRequestPayload
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal(http.MethodPost, request.Method)
		s.Equal("/search", request.URL.Path)
		s.Equal("Bearer tavily-secret", request.Header.Get("Authorization"))
		s.Require().NoError(json.NewDecoder(request.Body).Decode(&received))
		_, _ = writer.Write([]byte(`{"results":[{"title":"Result","url":"https://example.com","content":"Snippet","published_date":"2026-01-02"}],"usage":{"credits":1.5}}`))
	}))
	defer server.Close()

	after := time.Date(2026, time.January, 2, 1, 0, 0, 0, time.FixedZone("UTC+1", 3600))
	before := time.Date(2026, time.January, 3, 1, 0, 0, 0, time.FixedZone("UTC+1", 3600))
	response, err := s.client(server).Search(context.Background(), SearchRequest{
		Query:           "golang",
		Limit:           10,
		IncludeDomains:  []string{"go.dev"},
		ExcludeDomains:  []string{"example.com"},
		PublishedAfter:  &after,
		PublishedBefore: &before,
	})

	s.Require().NoError(err)
	s.Equal("golang", received.Query)
	s.Equal("basic", received.SearchDepth)
	s.False(received.AutoParameters)
	s.Equal("general", received.Topic)
	s.Equal(10, received.MaxResults)
	s.Equal([]string{"go.dev"}, received.IncludeDomains)
	s.Equal([]string{"example.com"}, received.ExcludeDomains)
	s.Equal("2026-01-02", received.StartDate)
	s.Equal("2026-01-03", received.EndDate)
	s.False(received.IncludeAnswer)
	s.False(received.IncludeRawContent)
	s.False(received.IncludeImages)
	s.True(received.IncludeUsage)
	s.Require().Len(response.Results, 1)
	s.Equal(1, response.Results[0].Rank)
	s.Equal("Snippet", response.Results[0].Snippet)
	s.Require().NotNil(response.Results[0].PublishedAt)
	s.Equal("2026-01-02T00:00:00Z", response.Results[0].PublishedAt.Format(time.RFC3339))
	s.Equal(1.5, s.metrics.Snapshot(provider.Key{Provider: provider.TAVILY, Action: provider.SEARCH}).CreditsUsed)
}

func (s *ClientSuite) TestExtractSendsConfiguredMarkdownAndRecordsUsage() {
	var received extractRequestPayload
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal(http.MethodPost, request.Method)
		s.Equal("/extract", request.URL.Path)
		s.Require().NoError(json.NewDecoder(request.Body).Decode(&received))
		_, _ = writer.Write([]byte(`{"results":[{"url":"https://example.com","raw_content":"# Document"}],"usage":{"credits":2}}`))
	}))
	defer server.Close()

	response, err := s.client(server).Extract(context.Background(), ContentRequest{URL: "https://example.com"})

	s.Require().NoError(err)
	s.Equal([]string{"https://example.com"}, received.URLs)
	s.Equal("basic", received.ExtractDepth)
	s.Equal("markdown", received.Format)
	s.False(received.IncludeImages)
	s.Equal(20.0, received.Timeout)
	s.True(received.IncludeUsage)
	s.Equal("# Document", response.Content)
	s.Equal(2.0, s.metrics.Snapshot(provider.Key{Provider: provider.TAVILY, Action: provider.EXTRACT}).CreditsUsed)
}

func (s *ClientSuite) TestExtractClassifiesFailedResultForFallback() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"results":[],"failed_results":[{"url":"https://example.com","error":"blocked"}]}`))
	}))
	defer server.Close()

	_, err := s.client(server).Extract(context.Background(), ContentRequest{URL: "https://example.com"})

	var failure *provider.Failure
	s.Require().True(errors.As(err, &failure))
	s.Equal(provider.REASON_UNAVAILABLE, failure.Reason)
	s.False(failure.Terminal)
}

func (s *ClientSuite) TestHTTPFailuresAreClassified() {
	for _, test := range []struct {
		status    int
		reason    provider.Reason
		retryable bool
	}{
		{status: http.StatusUnauthorized, reason: provider.REASON_UNAUTHORIZED},
		{status: http.StatusTooManyRequests, reason: provider.REASON_RATE_LIMITED, retryable: true},
		{status: 432, reason: provider.REASON_QUOTA},
		{status: 433, reason: provider.REASON_QUOTA},
		{status: http.StatusInternalServerError, reason: provider.REASON_TEMPORARY, retryable: true},
	} {
		s.Run(strconv.Itoa(test.status), func() {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
			}))

			_, err := s.client(server).Search(context.Background(), SearchRequest{Query: "golang", Limit: 1})
			server.Close()

			var failure *provider.Failure
			s.Require().True(errors.As(err, &failure))
			s.Equal(test.reason, failure.Reason)
			s.Equal(test.retryable, failure.Retryable)
		})
	}
}

func (s *ClientSuite) TestExtractTimeoutIsRetryable() {
	timeoutClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	client, err := New(s.settings, timeoutClient, timeoutClient, s.metrics, zap.NewNop())
	s.Require().NoError(err)

	_, err = client.Extract(context.Background(), ContentRequest{URL: "https://example.com"})

	var failure *provider.Failure
	s.Require().True(errors.As(err, &failure))
	s.Equal(provider.REASON_TIMEOUT, failure.Reason)
	s.True(failure.Retryable)
}

func (s *ClientSuite) TestConfigValidatesTavilySpecificSettings() {
	s.Equal("basic", s.settings.Search.SearchDepth)
	s.Equal("markdown", s.settings.Extract.Format)
	s.True(s.settings.Search.IncludeUsage)
	s.True(s.settings.Extract.IncludeUsage)

	s.settings.Search.SearchDepth = "invalid"
	s.EqualError(s.settings.Search.Validate(), "search_depth is invalid")
	s.settings.Extract.Format = "html"
	s.EqualError(s.settings.Extract.Validate(), "format is invalid")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}
