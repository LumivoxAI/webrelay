package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LumivoxAI/webrelay/internal/cache"
	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/content"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/LumivoxAI/webrelay/internal/urlpolicy"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

type ContentSuite struct {
	suite.Suite
	ctx   context.Context
	store *cache.Store
	cfg   config.Config
}

type fakeContentClient struct {
	calls int
	fetch func(context.Context, string, bool) (content.ProviderResponse, error)
}

func (c *fakeContentClient) Fetch(ctx context.Context, rawURL string, forceRefresh bool) (content.ProviderResponse, error) {
	c.calls++
	return c.fetch(ctx, rawURL, forceRefresh)
}

type publicResolver struct{}

func (publicResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
}

func TestContentSuite(t *testing.T) {
	suite.Run(t, new(ContentSuite))
}

func (s *ContentSuite) SetupTest() {
	s.ctx = context.Background()
	s.cfg = config.Default()
	s.cfg.Cache.Path = filepath.Join(s.T().TempDir(), "cache.db")
	s.cfg.Content.Providers = []string{"markdown_new", "exa"}
	store, err := cache.Open(s.ctx, s.cfg.Cache)
	require.NoError(s.T(), err)
	s.store = store
}

func (s *ContentSuite) TearDownTest() {
	require.NoError(s.T(), s.store.Close())
}

func (s *ContentSuite) TestResultUsesCachedEmbeddedDocumentBeforeProviders() {
	result := s.putResult("https://example.com/article", "exa")
	_, err := s.store.PutDocument(s.ctx, cache.Document{
		NormalizedURL: "https://example.com/article", OriginalURL: "https://example.com/article", Title: "Embedded", Content: "Привет😀мир",
		Provider: "exa_search", FetchedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC(), ContentHash: "hash",
	})
	s.Require().NoError(err)
	markdown := &fakeContentClient{fetch: func(context.Context, string, bool) (content.ProviderResponse, error) {
		return content.ProviderResponse{}, errors.New("provider must not be called")
	}}

	response := s.get(s.handler(map[provider.Key]*fakeContentClient{{Provider: provider.MARKDOWN_NEW, Action: provider.FETCH}: markdown}), "/v1/results/"+result.ID+"/content?offset=1&limit=2")

	s.Equal(http.StatusOK, response.Code)
	var body ContentResponse
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
	s.True(body.Cached)
	s.True(body.ContentIsUntrusted)
	s.Equal("ри", body.Content)
	s.Equal("exa", *body.SearchProvider)
	s.Equal("exa_search", body.ContentProvider)
	s.Zero(markdown.calls)
}

func (s *ContentSuite) TestFetchFallsBackAndForceRefreshSkipsCache() {
	_, err := s.store.PutDocument(s.ctx, cache.Document{
		NormalizedURL: "https://example.com/article", OriginalURL: "https://example.com/article", Content: "cached",
		Provider: "markdown_new", FetchedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC(), ContentHash: "hash",
	})
	s.Require().NoError(err)
	markdown := &fakeContentClient{fetch: func(context.Context, string, bool) (content.ProviderResponse, error) {
		return content.ProviderResponse{}, &provider.Failure{Reason: provider.REASON_TIMEOUT, Retryable: true}
	}}
	exa := &fakeContentClient{fetch: func(_ context.Context, rawURL string, forceRefresh bool) (content.ProviderResponse, error) {
		s.Equal("https://example.com/article", rawURL)
		s.True(forceRefresh)
		return content.ProviderResponse{Title: "Fresh", Content: "fresh content"}, nil
	}}

	response := s.postContent(s.handler(map[provider.Key]*fakeContentClient{
		{Provider: provider.MARKDOWN_NEW, Action: provider.FETCH}: markdown,
		{Provider: provider.EXA, Action: provider.CONTENTS}:       exa,
	}), `{"url":"https://example.com/article","force_refresh":true}`)

	s.Equal(http.StatusOK, response.Code)
	var body ContentResponse
	s.Require().NoError(json.Unmarshal(response.Body.Bytes(), &body))
	s.False(body.Cached)
	s.Equal("fresh content", body.Content)
	s.Equal("exa", body.ContentProvider)
	s.Equal(1, markdown.calls)
	s.Equal(1, exa.calls)
}

func (s *ContentSuite) TestDocumentPaginationAndErrors() {
	document, err := s.store.PutDocument(s.ctx, cache.Document{
		NormalizedURL: "https://example.com/article", OriginalURL: "https://example.com/article", Content: "аб😀в",
		Provider: "markdown_new", FetchedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC(), ContentHash: "hash",
	})
	s.Require().NoError(err)
	handler := s.handler(nil)

	page := s.get(handler, "/v1/documents/"+document.ID+"/content?offset=2&limit=1")
	outOfRange := s.get(handler, "/v1/documents/"+document.ID+"/content?offset=5")
	notFound := s.get(handler, "/v1/documents/missing/content")

	s.Equal(http.StatusOK, page.Code)
	var body ContentResponse
	s.Require().NoError(json.Unmarshal(page.Body.Bytes(), &body))
	s.Equal("😀", body.Content)
	s.True(body.Truncated)
	s.Equal(3, *body.NextOffset)
	s.Equal(http.StatusRequestedRangeNotSatisfiable, outOfRange.Code)
	s.Equal(http.StatusNotFound, notFound.Code)
}

func (s *ContentSuite) TestRejectsUnsafeURLAndReturnsAggregateFailures() {
	unsafe := s.postContent(s.handler(nil), `{"url":"http://127.0.0.1/"}`)
	markdown := &fakeContentClient{fetch: func(context.Context, string, bool) (content.ProviderResponse, error) {
		return content.ProviderResponse{}, &provider.Failure{Reason: provider.REASON_RATE_LIMITED}
	}}
	exa := &fakeContentClient{fetch: func(context.Context, string, bool) (content.ProviderResponse, error) {
		return content.ProviderResponse{}, &provider.Failure{Reason: provider.REASON_QUOTA}
	}}
	failed := s.postContent(s.handler(map[provider.Key]*fakeContentClient{
		{Provider: provider.MARKDOWN_NEW, Action: provider.FETCH}: markdown,
		{Provider: provider.EXA, Action: provider.CONTENTS}:       exa,
	}), `{"url":"https://example.com/article"}`)

	s.Equal(http.StatusUnprocessableEntity, unsafe.Code)
	s.Equal(http.StatusBadGateway, failed.Code)
	var body errorResponse
	s.Require().NoError(json.Unmarshal(failed.Body.Bytes(), &body))
	s.Equal(CODE_ALL_PROVIDERS_FAILED, body.Error.Code)
	s.Len(body.Error.Attempts, 2)
}

func (s *ContentSuite) putResult(rawURL, providerName string) *cache.SearchResult {
	entry, err := s.store.PutSearch(s.ctx, cache.SearchEntry{
		Key: "key-" + rawURL, OriginalQuery: "query", NormalizedQuery: "query", Parameters: []byte("{}"), Provider: providerName,
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC(),
		Results: []cache.SearchResult{{Rank: 1, URL: rawURL, NormalizedURL: rawURL, Title: "Article"}},
	})
	s.Require().NoError(err)
	return &entry.Results[0]
}

func (s *ContentSuite) handler(clients map[provider.Key]*fakeContentClient) http.Handler {
	initial := map[provider.Key]provider.State{}
	policies := map[provider.Key]provider.Policy{}
	contentClients := make(map[provider.Key]content.Client, len(clients))
	for _, key := range []provider.Key{{Provider: provider.MARKDOWN_NEW, Action: provider.FETCH}, {Provider: provider.EXA, Action: provider.CONTENTS}} {
		initial[key] = provider.STATE_AVAILABLE
		policies[key] = provider.Policy{MaxAttempts: 1, FailureThreshold: 2, Cooldown: time.Minute, QuotaCooldown: time.Minute, RateLimitCooldown: time.Minute}
		if client := clients[key]; client != nil {
			contentClients[key] = client
		}
	}
	workflow, err := content.New(s.cfg, s.store, provider.NewManager(initial, policies), contentClients, urlpolicy.New(publicResolver{}), zap.NewNop())
	s.Require().NoError(err)
	return NewHandler(zap.NewNop(), Dependencies{Content: workflow, MaxRequestBodyBytes: 1024})
}

func (s *ContentSuite) get(handler http.Handler, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func (s *ContentSuite) postContent(handler http.Handler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/fetch", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
