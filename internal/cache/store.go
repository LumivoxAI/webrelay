package cache

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/oklog/ulid/v2"
	_ "modernc.org/sqlite"
)

const DRIVER_NAME = "sqlite"

// Store is a persistent SQLite cache.
type Store struct {
	db      *sql.DB
	maxSize int64
	now     func() time.Time
	newID   func() string
}

// Open opens the cache database and applies its schema migrations.
func Open(ctx context.Context, cfg config.CacheConfig) (*Store, error) {
	db, err := sql.Open(DRIVER_NAME, cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("open cache database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{
		db:      db,
		maxSize: int64(cfg.MaxSizeMB) * 1024 * 1024,
		now:     time.Now,
		newID: func() string {
			return ulid.MustNew(ulid.Timestamp(time.Now()), cryptorand.Reader).String()
		},
	}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close closes the SQLite database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Ping verifies that the SQLite database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		`CREATE TABLE IF NOT EXISTS search_entries (
			id TEXT PRIMARY KEY,
			cache_key TEXT NOT NULL UNIQUE,
			original_query TEXT NOT NULL,
			normalized_query TEXT NOT NULL,
			parameters BLOB NOT NULL,
			provider TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS search_results (
			id TEXT PRIMARY KEY,
			search_id TEXT NOT NULL REFERENCES search_entries(id) ON DELETE CASCADE,
			rank INTEGER NOT NULL,
			url TEXT NOT NULL,
			normalized_url TEXT NOT NULL,
			title TEXT NOT NULL,
			snippet TEXT NOT NULL,
			published_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS documents (
			id TEXT PRIMARY KEY,
			normalized_url TEXT NOT NULL UNIQUE,
			original_url TEXT NOT NULL,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			provider TEXT NOT NULL,
			source_media_type TEXT,
			content_hash TEXT NOT NULL,
			fetched_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			content_size INTEGER NOT NULL,
			last_accessed_at INTEGER NOT NULL
		)`,
		"CREATE INDEX IF NOT EXISTS search_entries_expires_at_idx ON search_entries(expires_at)",
		"CREATE INDEX IF NOT EXISTS search_results_search_id_idx ON search_results(search_id)",
		"CREATE INDEX IF NOT EXISTS documents_expires_at_idx ON documents(expires_at)",
		"CREATE INDEX IF NOT EXISTS documents_last_accessed_at_idx ON documents(last_accessed_at)",
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate cache database: %w", err)
		}
	}
	return nil
}

// GetSearch returns a non-expired search entry by its cache key.
func (s *Store) GetSearch(ctx context.Context, key string) (*SearchEntry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, original_query, normalized_query, parameters, provider, created_at, expires_at
		FROM search_entries WHERE cache_key = ? AND expires_at > ?`, key, unixNanos(s.now()))
	entry := &SearchEntry{Key: key}
	var createdAt, expiresAt int64
	if err := row.Scan(&entry.ID, &entry.OriginalQuery, &entry.NormalizedQuery, &entry.Parameters, &entry.Provider, &createdAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get search entry: %w", err)
	}
	entry.CreatedAt = time.Unix(0, createdAt).UTC()
	entry.ExpiresAt = time.Unix(0, expiresAt).UTC()

	results, err := s.searchResults(ctx, entry.ID)
	if err != nil {
		return nil, err
	}
	entry.Results = results
	return entry, nil
}

// PutSearch replaces the cached entry for a cache key and all of its results.
func (s *Store) PutSearch(ctx context.Context, entry SearchEntry) (*SearchEntry, error) {
	if entry.ID == "" {
		entry.ID = s.newID()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin save search entry: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "DELETE FROM search_entries WHERE cache_key = ?", entry.Key); err != nil {
		return nil, fmt.Errorf("replace search entry: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO search_entries
		(id, cache_key, original_query, normalized_query, parameters, provider, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, entry.ID, entry.Key, entry.OriginalQuery, entry.NormalizedQuery,
		entry.Parameters, entry.Provider, unixNanos(entry.CreatedAt), unixNanos(entry.ExpiresAt)); err != nil {
		return nil, fmt.Errorf("save search entry: %w", err)
	}
	for index := range entry.Results {
		result := entry.Results[index]
		if result.ID == "" {
			result.ID = s.newID()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO search_results
			(id, search_id, rank, url, normalized_url, title, snippet, published_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, result.ID, entry.ID, result.Rank, result.URL, result.NormalizedURL,
			result.Title, result.Snippet, nullableTime(result.PublishedAt)); err != nil {
			return nil, fmt.Errorf("save search result: %w", err)
		}
		entry.Results[index].ID = result.ID
		entry.Results[index].SearchID = entry.ID
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit search entry: %w", err)
	}
	return &entry, nil
}

// GetResult returns a result only while its parent search entry is valid.
func (s *Store) GetResult(ctx context.Context, id string) (*SearchResult, error) {
	row := s.db.QueryRowContext(ctx, `SELECT r.id, r.search_id, r.rank, r.url, r.normalized_url, r.title, r.snippet, r.published_at, s.provider
		FROM search_results r JOIN search_entries s ON s.id = r.search_id
		WHERE r.id = ? AND s.expires_at > ?`, id, unixNanos(s.now()))
	result, err := scanSearchResultWithProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get search result: %w", err)
	}
	return result, nil
}

func scanSearchResultWithProvider(row rowScanner) (*SearchResult, error) {
	result := &SearchResult{}
	var publishedAt sql.NullInt64
	err := row.Scan(&result.ID, &result.SearchID, &result.Rank, &result.URL, &result.NormalizedURL, &result.Title, &result.Snippet, &publishedAt, &result.SearchProvider)
	if err != nil {
		return nil, err
	}
	if publishedAt.Valid {
		value := time.Unix(0, publishedAt.Int64).UTC()
		result.PublishedAt = &value
	}
	return result, nil
}

// GetDocumentByURL returns a non-expired document by its normalized URL.
func (s *Store) GetDocumentByURL(ctx context.Context, normalizedURL string) (*Document, error) {
	return s.getDocument(ctx, "normalized_url", normalizedURL)
}

// GetDocument returns a non-expired document by its stable ID.
func (s *Store) GetDocument(ctx context.Context, id string) (*Document, error) {
	return s.getDocument(ctx, "id", id)
}

// PutDocument creates or refreshes a document while preserving its stable ID.
func (s *Store) PutDocument(ctx context.Context, document Document) (*Document, error) {
	if document.NormalizedURL == "" {
		return nil, fmt.Errorf("document normalized URL is required")
	}
	if document.ID == "" {
		existing, err := s.documentIDByURL(ctx, document.NormalizedURL)
		if err != nil {
			return nil, err
		}
		document.ID = existing
		if document.ID == "" {
			document.ID = s.newID()
		}
	}
	if document.LastAccessedAt.IsZero() {
		document.LastAccessedAt = s.now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO documents
		(id, normalized_url, original_url, title, content, provider, source_media_type, content_hash, fetched_at, expires_at, content_size, last_accessed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(normalized_url) DO UPDATE SET
			original_url = excluded.original_url,
			title = CASE WHEN excluded.title <> '' THEN excluded.title ELSE documents.title END,
			content = excluded.content,
			provider = excluded.provider,
			source_media_type = excluded.source_media_type,
			content_hash = excluded.content_hash,
			fetched_at = excluded.fetched_at,
			expires_at = excluded.expires_at,
			content_size = excluded.content_size,
			last_accessed_at = excluded.last_accessed_at`, document.ID, document.NormalizedURL, document.OriginalURL,
		document.Title, document.Content, document.Provider, nullableString(document.SourceMediaType), document.ContentHash,
		unixNanos(document.FetchedAt), unixNanos(document.ExpiresAt), len([]rune(document.Content)), unixNanos(document.LastAccessedAt))
	if err != nil {
		return nil, fmt.Errorf("save document: %w", err)
	}
	return s.GetDocumentByURL(ctx, document.NormalizedURL)
}

func (s *Store) getDocument(ctx context.Context, field, value string) (*Document, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, normalized_url, original_url, title, content, provider, source_media_type, content_hash,
		fetched_at, expires_at, last_accessed_at FROM documents WHERE `+field+` = ? AND expires_at > ?`, value, unixNanos(s.now()))
	document, err := scanDocument(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get document: %w", err)
	}
	accessedAt := s.now().UTC()
	if _, err := s.db.ExecContext(ctx, "UPDATE documents SET last_accessed_at = ? WHERE id = ?", unixNanos(accessedAt), document.ID); err != nil {
		return nil, fmt.Errorf("update document access time: %w", err)
	}
	document.LastAccessedAt = accessedAt
	return document, nil
}

func (s *Store) documentIDByURL(ctx context.Context, normalizedURL string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM documents WHERE normalized_url = ?", normalizedURL).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup document: %w", err)
	}
	return id, nil
}

func (s *Store) searchResults(ctx context.Context, searchID string) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, search_id, rank, url, normalized_url, title, snippet, published_at
		FROM search_results WHERE search_id = ? ORDER BY rank`, searchID)
	if err != nil {
		return nil, fmt.Errorf("list search results: %w", err)
	}
	defer rows.Close()

	results := make([]SearchResult, 0)
	for rows.Next() {
		result, err := scanSearchResult(rows)
		if err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, *result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search results: %w", err)
	}
	return results, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanSearchResult(row rowScanner) (*SearchResult, error) {
	result := &SearchResult{}
	var publishedAt sql.NullInt64
	err := row.Scan(&result.ID, &result.SearchID, &result.Rank, &result.URL, &result.NormalizedURL, &result.Title, &result.Snippet, &publishedAt)
	if err != nil {
		return nil, err
	}
	if publishedAt.Valid {
		value := time.Unix(0, publishedAt.Int64).UTC()
		result.PublishedAt = &value
	}
	return result, nil
}

func scanDocument(row rowScanner) (*Document, error) {
	document := &Document{}
	var mediaType sql.NullString
	var fetchedAt, expiresAt, lastAccessedAt int64
	err := row.Scan(&document.ID, &document.NormalizedURL, &document.OriginalURL, &document.Title, &document.Content, &document.Provider,
		&mediaType, &document.ContentHash, &fetchedAt, &expiresAt, &lastAccessedAt)
	if err != nil {
		return nil, err
	}
	if mediaType.Valid {
		document.SourceMediaType = &mediaType.String
	}
	document.FetchedAt = time.Unix(0, fetchedAt).UTC()
	document.ExpiresAt = time.Unix(0, expiresAt).UTC()
	document.LastAccessedAt = time.Unix(0, lastAccessedAt).UTC()
	return document, nil
}

func unixNanos(value time.Time) int64 {
	return value.UTC().UnixNano()
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return unixNanos(*value)
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
