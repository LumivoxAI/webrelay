package cache

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type StoreSuite struct {
	suite.Suite
	ctx   context.Context
	path  string
	store *Store
	now   time.Time
}

func TestStoreSuite(t *testing.T) {
	suite.Run(t, new(StoreSuite))
}

func (suite *StoreSuite) SetupTest() {
	suite.ctx = context.Background()
	suite.path = filepath.Join(suite.T().TempDir(), "cache.db")
	suite.now = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	suite.store = suite.openStore(10)
}

func (suite *StoreSuite) TearDownTest() {
	require.NoError(suite.T(), suite.store.Close())
}

func (suite *StoreSuite) TestSearchAndDocumentTTLAreIndependent() {
	search := suite.putSearch("key", suite.now.Add(time.Minute))
	document := suite.putDocument("https://example.com/article", suite.now.Add(time.Hour), "content")

	suite.now = suite.now.Add(2 * time.Minute)
	entry, err := suite.store.GetSearch(suite.ctx, "key")
	require.NoError(suite.T(), err)
	suite.Nil(entry)

	result, err := suite.store.GetResult(suite.ctx, search.Results[0].ID)
	require.NoError(suite.T(), err)
	suite.Nil(result)

	cached, err := suite.store.GetDocument(suite.ctx, document.ID)
	require.NoError(suite.T(), err)
	require.NotNil(suite.T(), cached)
	suite.Equal(document.ID, cached.ID)
}

func (suite *StoreSuite) TestDataSurvivesReopen() {
	search := suite.putSearch("key", suite.now.Add(time.Hour))
	document := suite.putDocument("https://example.com/article", suite.now.Add(time.Hour), "content")
	require.NoError(suite.T(), suite.store.Close())
	suite.store = suite.openStore(10)

	entry, err := suite.store.GetSearch(suite.ctx, "key")
	require.NoError(suite.T(), err)
	require.NotNil(suite.T(), entry)
	suite.Equal(search.ID, entry.ID)
	suite.Len(entry.Results, 1)

	cached, err := suite.store.GetDocument(suite.ctx, document.ID)
	require.NoError(suite.T(), err)
	require.NotNil(suite.T(), cached)
	suite.Equal("content", cached.Content)
}

func (suite *StoreSuite) TestDocumentRefreshKeepsIDAndUpdatesFields() {
	first := suite.putDocument("https://example.com/article", suite.now.Add(time.Hour), "first")
	suite.now = suite.now.Add(time.Minute)
	refreshed, err := suite.store.PutDocument(suite.ctx, Document{
		NormalizedURL: "https://example.com/article",
		OriginalURL:   "https://example.com/article",
		Title:         "updated",
		Content:       "second",
		Provider:      "markdown_new",
		ContentHash:   "hash-2",
		FetchedAt:     suite.now,
		ExpiresAt:     suite.now.Add(time.Hour),
	})
	require.NoError(suite.T(), err)
	suite.Equal(first.ID, refreshed.ID)
	suite.Equal("second", refreshed.Content)
	suite.Equal("updated", refreshed.Title)
}

func (suite *StoreSuite) TestDocumentUsesNormalizedURLAsKey() {
	first := suite.putDocument("https://example.com/article", suite.now.Add(time.Hour), "first")
	second, err := suite.store.PutDocument(suite.ctx, Document{
		NormalizedURL: "https://example.com/article",
		OriginalURL:   "https://example.com/article#section",
		Title:         "Article",
		Content:       "second",
		Provider:      "exa_search",
		ContentHash:   "hash-2",
		FetchedAt:     suite.now,
		ExpiresAt:     suite.now.Add(time.Hour),
	})
	require.NoError(suite.T(), err)
	suite.Equal(first.ID, second.ID)

	stored, err := suite.store.GetDocumentByURL(suite.ctx, "https://example.com/article")
	require.NoError(suite.T(), err)
	require.NotNil(suite.T(), stored)
	suite.Equal("second", stored.Content)
}

func (suite *StoreSuite) TestSearchReplacementRemovesPreviousResults() {
	first := suite.putSearch("key", suite.now.Add(time.Hour))
	replacement, err := suite.store.PutSearch(suite.ctx, SearchEntry{
		Key:             "key",
		OriginalQuery:   "replacement",
		NormalizedQuery: "replacement",
		Parameters:      []byte(`{"limit":1}`),
		Provider:        "brave",
		CreatedAt:       suite.now,
		ExpiresAt:       suite.now.Add(time.Hour),
		Results: []SearchResult{{
			Rank:          1,
			URL:           "https://example.com/replacement",
			NormalizedURL: "https://example.com/replacement",
			Title:         "Replacement",
			Snippet:       "Snippet",
		}},
	})
	require.NoError(suite.T(), err)

	oldResult, err := suite.store.GetResult(suite.ctx, first.Results[0].ID)
	require.NoError(suite.T(), err)
	suite.Nil(oldResult)
	entry, err := suite.store.GetSearch(suite.ctx, "key")
	require.NoError(suite.T(), err)
	require.NotNil(suite.T(), entry)
	suite.Equal(replacement.ID, entry.ID)
	suite.Equal("brave", entry.Provider)
}

func (suite *StoreSuite) TestCleanupDeletesExpiredDataAndPreservesValidData() {
	suite.putSearch("expired", suite.now.Add(time.Minute))
	validSearch := suite.putSearch("valid", suite.now.Add(time.Hour))
	expiredDocument := suite.putDocument("https://example.com/expired", suite.now.Add(time.Minute), "expired")
	validDocument := suite.putDocument("https://example.com/valid", suite.now.Add(time.Hour), "valid")
	suite.now = suite.now.Add(2 * time.Minute)

	require.NoError(suite.T(), suite.store.Cleanup(suite.ctx))

	entry, err := suite.store.GetSearch(suite.ctx, "expired")
	require.NoError(suite.T(), err)
	suite.Nil(entry)
	result, err := suite.store.GetResult(suite.ctx, validSearch.Results[0].ID)
	require.NoError(suite.T(), err)
	require.NotNil(suite.T(), result)

	cachedExpired, err := suite.store.GetDocument(suite.ctx, expiredDocument.ID)
	require.NoError(suite.T(), err)
	suite.Nil(cachedExpired)
	cachedValid, err := suite.store.GetDocument(suite.ctx, validDocument.ID)
	require.NoError(suite.T(), err)
	require.NotNil(suite.T(), cachedValid)
}

func (suite *StoreSuite) TestCleanupEvictsLeastRecentlyUsedDocument() {
	require.NoError(suite.T(), suite.store.Close())
	suite.store = suite.openStore(1)
	first := suite.putDocument("https://example.com/first", suite.now.Add(time.Hour), strings.Repeat("a", 700_000))
	suite.now = suite.now.Add(time.Minute)
	second := suite.putDocument("https://example.com/second", suite.now.Add(time.Hour), strings.Repeat("b", 700_000))

	require.NoError(suite.T(), suite.store.Cleanup(suite.ctx))

	evicted, err := suite.store.GetDocument(suite.ctx, first.ID)
	require.NoError(suite.T(), err)
	suite.Nil(evicted)
	kept, err := suite.store.GetDocument(suite.ctx, second.ID)
	require.NoError(suite.T(), err)
	require.NotNil(suite.T(), kept)
}

func (suite *StoreSuite) openStore(maxSizeMB int) *Store {
	store, err := Open(suite.ctx, config.CacheConfig{
		Path:      suite.path,
		MaxSizeMB: maxSizeMB,
	})
	require.NoError(suite.T(), err)
	store.now = func() time.Time { return suite.now }
	return store
}

func (suite *StoreSuite) putSearch(key string, expiresAt time.Time) *SearchEntry {
	entry, err := suite.store.PutSearch(suite.ctx, SearchEntry{
		Key:             key,
		OriginalQuery:   "Go cache",
		NormalizedQuery: "Go cache",
		Parameters:      []byte(`{"limit":10}`),
		Provider:        "exa",
		CreatedAt:       suite.now,
		ExpiresAt:       expiresAt,
		Results: []SearchResult{{
			Rank:          1,
			URL:           "https://example.com/article",
			NormalizedURL: "https://example.com/article",
			Title:         "Article",
			Snippet:       "Snippet",
		}},
	})
	require.NoError(suite.T(), err)
	return entry
}

func (suite *StoreSuite) putDocument(url string, expiresAt time.Time, content string) *Document {
	document, err := suite.store.PutDocument(suite.ctx, Document{
		NormalizedURL: url,
		OriginalURL:   url,
		Title:         "Article",
		Content:       content,
		Provider:      "exa_search",
		ContentHash:   "hash",
		FetchedAt:     suite.now,
		ExpiresAt:     expiresAt,
	})
	require.NoError(suite.T(), err)
	return document
}
