package tinyfish

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
	settings config.TinyFishConfig
}

func (s *ClientSuite) SetupTest() {
	s.settings = config.DefaultTinyFishConfig()
	s.settings.APIKey = "tinyfish-secret"
	s.settings.Search.Location = "RU"
}

func (s *ClientSuite) client(server *httptest.Server) *Client {
	client, err := NewAtURLs(server.URL+"/search", server.URL+"/fetch", s.settings, server.Client(), server.Client(), zap.NewNop())
	s.Require().NoError(err)
	return client
}

func (s *ClientSuite) TestSearchSendsFiltersAndPaginatesFromZero() {
	pages := make([]int, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal(http.MethodGet, request.Method)
		s.Equal("tinyfish-secret", request.Header.Get("X-API-Key"))
		values := request.URL.Query()
		page := values.Get("page")
		pages = append(pages, map[string]int{"0": 0, "1": 1}[page])
		s.Equal("golang", values.Get("query"))
		s.Equal("RU", values.Get("location"))
		s.Equal("en", values.Get("language"))
		s.Equal("web", values.Get("domain_type"))
		s.Equal("go.dev,github.com", values.Get("include_domains"))
		s.Equal("example.com", values.Get("exclude_domains"))
		s.Equal("2026-01-02", values.Get("after_date"))
		s.Equal("2026-01-03", values.Get("before_date"))

		results := make([]searchResultPayload, 10)
		if page == "1" {
			results = results[:2]
		}
		for index := range results {
			results[index] = searchResultPayload{Position: index + 1, Title: "Result", URL: "https://example.com/page", Snippet: "Snippet", Date: "2026-01-02"}
		}
		s.Require().NoError(json.NewEncoder(writer).Encode(searchResponsePayload{Results: results}))
	}))
	defer server.Close()

	after := time.Date(2026, time.January, 2, 1, 0, 0, 0, time.FixedZone("UTC+1", 3600))
	before := time.Date(2026, time.January, 3, 1, 0, 0, 0, time.FixedZone("UTC+1", 3600))
	response, err := s.client(server).Search(context.Background(), SearchRequest{
		Query:           "golang",
		Limit:           12,
		IncludeDomains:  []string{"go.dev", "github.com"},
		ExcludeDomains:  []string{"example.com"},
		PublishedAfter:  &after,
		PublishedBefore: &before,
	})

	s.Require().NoError(err)
	s.Equal([]int{0, 1}, pages)
	s.Len(response.Results, 12)
	s.Equal(1, response.Results[0].Rank)
	s.Equal(12, response.Results[11].Rank)
	s.Require().NotNil(response.Results[0].PublishedAt)
	s.Equal("2026-01-02T00:00:00Z", response.Results[0].PublishedAt.Format(time.RFC3339))
}

func (s *ClientSuite) TestSearchStopsAfterShortFirstPage() {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = writer.Write([]byte(`{"results":[{"position":1,"title":"Result","url":"https://example.com","snippet":"Snippet"}]}`))
	}))
	defer server.Close()

	response, err := s.client(server).Search(context.Background(), SearchRequest{Query: "golang", Limit: 20})

	s.Require().NoError(err)
	s.Equal(1, calls)
	s.Len(response.Results, 1)
}

func (s *ClientSuite) TestSearchStopsAtMaximumPage() {
	pages := make([]int, 0, MAX_PAGE+1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		page, err := strconv.Atoi(request.URL.Query().Get("page"))
		s.Require().NoError(err)
		pages = append(pages, page)
		results := make([]searchResultPayload, PAGE_SIZE)
		for index := range results {
			results[index] = searchResultPayload{URL: "https://example.com", Title: "Result"}
		}
		s.Require().NoError(json.NewEncoder(writer).Encode(searchResponsePayload{Results: results}))
	}))
	defer server.Close()

	response, err := s.client(server).Search(context.Background(), SearchRequest{Query: "golang", Limit: 200})

	s.Require().NoError(err)
	s.Equal([]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, pages)
	s.Len(response.Results, 110)
}

func (s *ClientSuite) TestFetchSendsMarkdownAndGatewayTTL() {
	var received fetchRequestPayload
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal(http.MethodPost, request.Method)
		s.Equal("tinyfish-secret", request.Header.Get("X-API-Key"))
		s.Require().NoError(json.NewDecoder(request.Body).Decode(&received))
		_, _ = writer.Write([]byte(`{"results":[{"url":"https://example.com","final_url":"https://www.example.com/","title":"Document","published_date":"2026-01-02T03:04:05Z","text":"# Document"}],"errors":[]}`))
	}))
	defer server.Close()

	response, err := s.client(server).Fetch(context.Background(), ContentRequest{URL: "https://example.com", DocumentTTL: 6 * time.Hour})

	s.Require().NoError(err)
	s.Equal([]string{"https://example.com"}, received.URLs)
	s.Equal("markdown", received.Format)
	s.False(received.Links)
	s.False(received.ImageLinks)
	s.Require().NotNil(received.TTL)
	s.Equal(21600, *received.TTL)
	s.Equal(20000, received.PerURLTimeoutMS)
	s.Equal("https://www.example.com/", response.URL)
	s.Equal("Document", response.Title)
	s.Equal("# Document", response.Content)
	s.Require().NotNil(response.PublishedAt)
	s.Equal("2026-01-02T03:04:05Z", response.PublishedAt.Format(time.RFC3339))
}

func (s *ClientSuite) TestFetchForceRefreshUsesZeroTTL() {
	var received fetchRequestPayload
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Require().NoError(json.NewDecoder(request.Body).Decode(&received))
		_, _ = writer.Write([]byte(`{"results":[{"url":"https://example.com","final_url":"https://example.com","text":"Content"}],"errors":[]}`))
	}))
	defer server.Close()

	_, err := s.client(server).Fetch(context.Background(), ContentRequest{URL: "https://example.com", DocumentTTL: 6 * time.Hour, ForceRefresh: true})

	s.Require().NoError(err)
	s.Require().NotNil(received.TTL)
	s.Zero(*received.TTL)
}

func (s *ClientSuite) TestFetchClassifiesPerURLFailures() {
	for _, test := range []struct {
		name     string
		category string
		terminal bool
	}{
		{name: "not found", category: "page_not_found", terminal: true},
		{name: "blocked", category: "bot_blocked"},
		{name: "timeout", category: "timeout"},
	} {
		s.Run(test.name, func() {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte(`{"results":[],"errors":[{"url":"https://example.com","error":"` + test.category + `"}]}`))
			}))

			_, err := s.client(server).Fetch(context.Background(), ContentRequest{URL: "https://example.com"})
			server.Close()

			var failure *provider.Failure
			s.Require().True(errors.As(err, &failure))
			s.Equal(provider.REASON_UNAVAILABLE, failure.Reason)
			s.Equal(test.terminal, failure.Terminal)
		})
	}
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
		{status: http.StatusNotFound, reason: provider.REASON_MISCONFIGURED},
		{status: http.StatusTooManyRequests, reason: provider.REASON_RATE_LIMITED, retryable: true},
		{status: http.StatusInternalServerError, reason: provider.REASON_TEMPORARY, retryable: true},
		{status: http.StatusServiceUnavailable, reason: provider.REASON_TEMPORARY, retryable: true},
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

func (s *ClientSuite) TestTimeoutIsRetryable() {
	timeoutClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	client, err := New(s.settings, timeoutClient, timeoutClient, zap.NewNop())
	s.Require().NoError(err)

	_, err = client.Fetch(context.Background(), ContentRequest{URL: "https://example.com"})

	var failure *provider.Failure
	s.Require().True(errors.As(err, &failure))
	s.Equal(provider.REASON_TIMEOUT, failure.Reason)
	s.True(failure.Retryable)
}

func (s *ClientSuite) TestConfigValidatesTinyFishSpecificSettings() {
	s.Equal("web", s.settings.Search.DomainType)
	s.Equal("markdown", s.settings.Fetch.Format)
	s.True(s.settings.Fetch.UseGatewayTTL)

	s.settings.Search.DomainType = "invalid"
	s.EqualError(s.settings.Search.Validate(), "domain_type is invalid")
	s.settings.Fetch.Format = "html"
	s.EqualError(s.settings.Fetch.Validate(), "format must be markdown")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}
