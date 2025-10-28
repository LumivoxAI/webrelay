package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LumivoxAI/webrelay/internal/cache"
	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/LumivoxAI/webrelay/internal/search"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

type SearchSuite struct {
	suite.Suite
	ctx   context.Context
	store *cache.Store
	cfg   config.Config
}

type fakeSearchClient struct {
	calls  int
	search func(context.Context, search.Request) (search.Response, error)
}

func (c *fakeSearchClient) Search(ctx context.Context, request search.Request) (search.Response, error) {
	c.calls++
	return c.search(ctx, request)
}

func TestSearchSuite(t *testing.T) {
	suite.Run(t, new(SearchSuite))
}

func (s *SearchSuite) SetupTest() {
	s.ctx = context.Background()
	s.cfg = config.Default()
	s.cfg.Cache.Path = filepath.Join(s.T().TempDir(), "cache.db")
	s.cfg.Search.Providers = []string{"exa", "brave"}
	store, err := cache.Open(s.ctx, s.cfg.Cache)
	require.NoError(s.T(), err)
	s.store = store
}

func (s *SearchSuite) TearDownTest() {
	require.NoError(s.T(), s.store.Close())
}

func (s *SearchSuite) TestExaSuccessDoesNotExposeEmbeddedContentAndCachesIt() {
	exa := &fakeSearchClient{search: func(context.Context, search.Request) (search.Response, error) {
		return search.Response{Results: []search.Result{{URL: "https://example.com/article#section", Title: "Article", Snippet: "Snippet", EmbeddedContent: "private full page text"}}}, nil
	}}
	handler := s.handler(exa, nil)

	response := s.post(handler, `{"query":"  Go   cache  "}`)

	s.Equal(http.StatusOK, response.Code)
	var body SearchResponse
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
	s.Equal("Go cache", body.Query)
	s.Equal("exa", body.Provider)
	s.False(body.Cached)
	s.Len(body.Results, 1)
	s.Equal("https://example.com/article#section", body.Results[0].URL)
	s.NotContains(response.Body.String(), "private full page text")
	document, err := s.store.GetDocumentByURL(s.ctx, "https://example.com/article")
	s.Require().NoError(err)
	s.Require().NotNil(document)
	s.Equal("private full page text", document.Content)
	s.Equal("exa_search", document.Provider)
}

func (s *SearchSuite) TestFallsBackForExaFailures() {
	for _, testCase := range []struct {
		name    string
		failure *provider.Failure
	}{
		{name: "timeout", failure: &provider.Failure{Reason: provider.REASON_TIMEOUT, Retryable: true}},
		{name: "rate limited", failure: &provider.Failure{Reason: provider.REASON_RATE_LIMITED}},
		{name: "quota exhausted", failure: &provider.Failure{Reason: provider.REASON_QUOTA}},
		{name: "unauthorized", failure: &provider.Failure{Reason: provider.REASON_UNAUTHORIZED}},
	} {
		s.Run(testCase.name, func() {
			exa := &fakeSearchClient{search: func(context.Context, search.Request) (search.Response, error) {
				return search.Response{}, testCase.failure
			}}
			brave := &fakeSearchClient{search: successfulSearch}
			handler, manager := s.handlerWithManager(exa, brave)

			response := s.post(handler, `{"query":"fallback","force_refresh":true}`)

			s.Equal(http.StatusOK, response.Code)
			s.Equal(1, exa.calls)
			s.Equal(1, brave.calls)
			if testCase.failure.Reason == provider.REASON_UNAUTHORIZED {
				s.Equal(provider.STATE_MISCONFIGURED, manager.State(provider.Key{Provider: provider.EXA, Action: provider.SEARCH}))
			}
		})
	}
}

func (s *SearchSuite) TestEmptyAndShortExaResultsDoNotFallback() {
	for _, results := range [][]search.Result{
		nil,
		{{URL: "https://example.com/only", Title: "Only"}},
	} {
		exa := &fakeSearchClient{search: func(context.Context, search.Request) (search.Response, error) {
			return search.Response{Results: results}, nil
		}}
		brave := &fakeSearchClient{search: successfulSearch}

		response := s.post(s.handler(exa, brave), `{"query":"no fallback","limit":10,"force_refresh":true}`)

		s.Equal(http.StatusOK, response.Code)
		s.Equal(1, exa.calls)
		s.Zero(brave.calls)
	}
}

func (s *SearchSuite) TestCacheAndForceRefresh() {
	exa := &fakeSearchClient{search: successfulSearch}
	handler := s.handler(exa, nil)

	first := s.post(handler, `{"query":"cache test"}`)
	second := s.post(handler, `{"query":" cache  test "}`)
	refreshed := s.post(handler, `{"query":"cache test","force_refresh":true}`)

	s.Equal(http.StatusOK, first.Code)
	s.Equal(http.StatusOK, second.Code)
	s.Equal(http.StatusOK, refreshed.Code)
	var cached SearchResponse
	s.Require().NoError(json.Unmarshal(second.Body.Bytes(), &cached))
	s.True(cached.Cached)
	s.Equal(2, exa.calls)
}

func (s *SearchSuite) TestValidatesPublicSearchRequest() {
	handler := s.handler(&fakeSearchClient{search: successfulSearch}, nil)
	longQuery := strings.Repeat("a", 401)
	tooManyWords := strings.TrimSpace(strings.Repeat("word ", 51))
	for _, testCase := range []struct {
		name string
		body string
		code Code
	}{
		{name: "empty query", body: `{"query":"   "}`, code: CODE_INVALID_QUERY},
		{name: "long query", body: `{"query":"` + longQuery + `"}`, code: CODE_INVALID_QUERY},
		{name: "many words", body: `{"query":"` + tooManyWords + `"}`, code: CODE_INVALID_QUERY},
		{name: "large limit", body: `{"query":"valid","limit":21}`, code: CODE_INVALID_REQUEST},
		{name: "overlapping domains", body: `{"query":"valid","include_domains":["GitHub.com"],"exclude_domains":["github.com"]}`, code: CODE_INVALID_REQUEST},
	} {
		s.Run(testCase.name, func() {
			response := s.post(handler, testCase.body)
			s.Equal(http.StatusBadRequest, response.Code)
			var body errorResponse
			s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
			s.Equal(testCase.code, body.Error.Code)
		})
	}
}

func (s *SearchSuite) TestNormalizesDomainFilters() {
	exa := &fakeSearchClient{search: func(_ context.Context, request search.Request) (search.Response, error) {
		s.Equal([]string{"github.com", "go.dev"}, request.IncludeDomains)
		s.Equal([]string{"example.com"}, request.ExcludeDomains)
		return successfulSearch(context.Background(), request)
	}}

	response := s.post(s.handler(exa, nil), `{"query":"domains","include_domains":["GitHub.COM","go.dev","github.com"],"exclude_domains":["EXAMPLE.com"]}`)

	s.Equal(http.StatusOK, response.Code)
}

func (s *SearchSuite) handler(exa, brave *fakeSearchClient) http.Handler {
	handler, _ := s.handlerWithManager(exa, brave)
	return handler
}

func (s *SearchSuite) handlerWithManager(exa, brave *fakeSearchClient) (http.Handler, *provider.Manager) {
	initial := map[provider.Key]provider.State{
		{Provider: provider.EXA, Action: provider.SEARCH}:   provider.STATE_AVAILABLE,
		{Provider: provider.BRAVE, Action: provider.SEARCH}: provider.STATE_AVAILABLE,
	}
	policies := map[provider.Key]provider.Policy{
		{Provider: provider.EXA, Action: provider.SEARCH}:   {MaxAttempts: 1, FailureThreshold: 1, Cooldown: time.Minute, QuotaCooldown: time.Minute, RateLimitCooldown: time.Minute},
		{Provider: provider.BRAVE, Action: provider.SEARCH}: {MaxAttempts: 1, FailureThreshold: 1, Cooldown: time.Minute, QuotaCooldown: time.Minute, RateLimitCooldown: time.Minute},
	}
	manager := provider.NewManager(initial, policies)
	clients := make(map[provider.Name]search.Client)
	if exa != nil {
		clients[provider.EXA] = exa
	}
	if brave != nil {
		clients[provider.BRAVE] = brave
	}
	workflow, err := search.New(s.cfg, s.store, manager, clients, zap.NewNop())
	s.Require().NoError(err)
	return NewHandler(zap.NewNop(), Dependencies{Search: workflow, MaxRequestBodyBytes: 1024}), manager
}

func (s *SearchSuite) post(handler http.Handler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func successfulSearch(context.Context, search.Request) (search.Response, error) {
	return search.Response{Results: []search.Result{{URL: "https://example.com/article", Title: "Article", Snippet: "Snippet"}}}, nil
}
