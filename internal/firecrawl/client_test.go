package firecrawl

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
	settings config.FirecrawlConfig
	metrics  *provider.Metrics
}

func (s *ClientSuite) SetupTest() {
	s.settings = config.DefaultFirecrawlConfig()
	s.settings.APIKey = "firecrawl-secret"
	s.metrics = provider.NewMetrics(zap.NewNop())
}

func (s *ClientSuite) client(server *httptest.Server) *Client {
	client, err := NewAtURL(server.URL+"/", s.settings, server.Client(), server.Client(), s.metrics, zap.NewNop())
	s.Require().NoError(err)
	client.now = func() time.Time { return time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC) }
	return client
}

func (s *ClientSuite) TestSearchSendsFiltersDatesAndRecordsCredits() {
	var received searchRequestPayload
	var raw map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal(http.MethodPost, request.Method)
		s.Equal("/v2/search", request.URL.Path)
		s.Equal("Bearer firecrawl-secret", request.Header.Get("Authorization"))
		s.Require().NoError(json.NewDecoder(request.Body).Decode(&raw))
		encoded, err := json.Marshal(raw)
		s.Require().NoError(err)
		s.Require().NoError(json.Unmarshal(encoded, &received))
		_, hasScrapeOptions := raw["scrapeOptions"]
		s.False(hasScrapeOptions)
		_, _ = writer.Write([]byte(`{"success":true,"data":{"web":[{"title":"Result","url":"https://example.com","description":"Snippet"}]},"creditsUsed":2}`))
	}))
	defer server.Close()

	after := time.Date(2026, time.January, 2, 1, 0, 0, 0, time.FixedZone("UTC+1", 3600))
	before := time.Date(2026, time.January, 4, 1, 0, 0, 0, time.FixedZone("UTC+1", 3600))
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
	s.Equal(10, received.Limit)
	s.Equal([]string{"go.dev"}, received.IncludeDomains)
	s.Equal([]string{"example.com"}, received.ExcludeDomains)
	s.Equal("cdr:1,cd_min:01/02/2026,cd_max:01/04/2026", received.TBS)
	s.Require().Len(response.Results, 1)
	s.Equal(1, response.Results[0].Rank)
	s.Equal("Snippet", response.Results[0].Snippet)
	s.Equal(2.0, s.metrics.Snapshot(provider.Key{Provider: provider.FIRECRAWL, Action: provider.SEARCH}).CreditsUsed)
}

func (s *ClientSuite) TestSearchDateMapping() {
	after := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, time.January, 4, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		after    *time.Time
		before   *time.Time
		expected string
	}{
		{name: "after only", after: &after, expected: "cdr:1,cd_min:01/02/2026,cd_max:01/05/2026"},
		{name: "before only", before: &before, expected: ""},
		{name: "both bounds", after: &after, before: &before, expected: "cdr:1,cd_min:01/02/2026,cd_max:01/04/2026"},
	} {
		s.Run(test.name, func() {
			var received searchRequestPayload
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				s.Require().NoError(json.NewDecoder(request.Body).Decode(&received))
				_, _ = writer.Write([]byte(`{"success":true,"data":{"web":[]}}`))
			}))
			defer server.Close()

			_, err := s.client(server).Search(context.Background(), SearchRequest{Query: "golang", Limit: 1, PublishedAfter: test.after, PublishedBefore: test.before})

			s.Require().NoError(err)
			s.Equal(test.expected, received.TBS)
		})
	}
}

func (s *ClientSuite) TestScrapeSendsMarkdownAndRecordsCredits() {
	var received scrapeRequestPayload
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal(http.MethodPost, request.Method)
		s.Equal("/v2/scrape", request.URL.Path)
		s.Require().NoError(json.NewDecoder(request.Body).Decode(&received))
		_, _ = writer.Write([]byte(`{"success":true,"data":{"markdown":"# Document","metadata":{"title":"Document","url":"https://example.com/final"}},"creditsUsed":3}`))
	}))
	defer server.Close()

	response, err := s.client(server).Scrape(context.Background(), ContentRequest{URL: "https://example.com", DocumentTTL: 6 * time.Hour})

	s.Require().NoError(err)
	s.Equal("https://example.com", received.URL)
	s.Equal([]string{"markdown"}, received.Formats)
	s.True(received.OnlyMainContent)
	s.False(received.Lockdown)
	s.Equal(int((6 * time.Hour).Milliseconds()), received.MaxAge)
	s.Equal("https://example.com/final", response.URL)
	s.Equal("Document", response.Title)
	s.Equal("# Document", response.Content)
	s.Equal(3.0, s.metrics.Snapshot(provider.Key{Provider: provider.FIRECRAWL, Action: provider.SCRAPE}).CreditsUsed)
}

func (s *ClientSuite) TestScrapeRefreshDisablesProviderCache() {
	var received scrapeRequestPayload
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Require().NoError(json.NewDecoder(request.Body).Decode(&received))
		_, _ = writer.Write([]byte(`{"success":true,"data":{"markdown":"# Document"}}`))
	}))
	defer server.Close()

	_, err := s.client(server).Scrape(context.Background(), ContentRequest{URL: "https://example.com", DocumentTTL: time.Hour, ForceRefresh: true})

	s.Require().NoError(err)
	s.Equal(0, received.MaxAge)
}

func (s *ClientSuite) TestHTTPFailuresAreClassified() {
	for _, test := range []struct {
		status    int
		reason    provider.Reason
		retryable bool
	}{
		{status: http.StatusUnauthorized, reason: provider.REASON_UNAUTHORIZED},
		{status: http.StatusPaymentRequired, reason: provider.REASON_QUOTA},
		{status: http.StatusForbidden, reason: provider.REASON_FORBIDDEN},
		{status: http.StatusRequestTimeout, reason: provider.REASON_TIMEOUT, retryable: true},
		{status: http.StatusTooManyRequests, reason: provider.REASON_RATE_LIMITED, retryable: true},
		{status: http.StatusInternalServerError, reason: provider.REASON_TEMPORARY, retryable: true},
		{status: http.StatusBadGateway, reason: provider.REASON_TEMPORARY, retryable: true},
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

func (s *ClientSuite) TestRateLimitUsesRetryAfter() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "30")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := s.client(server).Search(context.Background(), SearchRequest{Query: "golang", Limit: 1})

	var failure *provider.Failure
	s.Require().True(errors.As(err, &failure))
	s.Equal(30*time.Second, failure.Cooldown)
}

func (s *ClientSuite) TestScrapeEmptyContentAllowsFallback() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"success":true,"data":{"markdown":" "}}`))
	}))
	defer server.Close()

	_, err := s.client(server).Scrape(context.Background(), ContentRequest{URL: "https://example.com"})

	var failure *provider.Failure
	s.Require().True(errors.As(err, &failure))
	s.Equal(provider.REASON_UNAVAILABLE, failure.Reason)
	s.False(failure.Terminal)
}

func (s *ClientSuite) TestScrapeTimeoutIsRetryable() {
	timeoutClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	client, err := New(s.settings, timeoutClient, timeoutClient, s.metrics, zap.NewNop())
	s.Require().NoError(err)

	_, err = client.Scrape(context.Background(), ContentRequest{URL: "https://example.com"})

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
