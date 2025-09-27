package cache

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Cleanup removes expired data and least-recently-used documents when needed.
func (s *Store) Cleanup(ctx context.Context) error {
	expiredDeleted, err := s.deleteExpired(ctx)
	if err != nil {
		return err
	}
	orphansDeleted, err := s.deleteOrphanedResults(ctx)
	if err != nil {
		return err
	}
	if err := s.checkpoint(ctx); err != nil {
		return err
	}
	hasFreePages, err := s.databaseHasFreePages(ctx)
	if err != nil {
		return err
	}
	if (expiredDeleted || orphansDeleted) && hasFreePages {
		if err := s.vacuum(ctx); err != nil {
			return err
		}
	}

	size, err := s.databaseSize(ctx)
	if err != nil {
		return err
	}
	for size > s.maxSize {
		deleted, err := s.deleteLeastRecentlyUsedDocument(ctx)
		if err != nil {
			return err
		}
		if !deleted {
			break
		}
		if err := s.checkpoint(ctx); err != nil {
			return err
		}
		if err := s.vacuum(ctx); err != nil {
			return err
		}
		size, err = s.databaseSize(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

// StartCleanupWorker starts a cleanup loop and returns after ctx is cancelled.
func (s *Store) StartCleanupWorker(ctx context.Context, interval time.Duration, logger *zap.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Cleanup(ctx); err != nil && ctx.Err() == nil {
				logger.Error("clean cache", zap.Error(err))
			}
		}
	}
}

func (s *Store) deleteExpired(ctx context.Context) (bool, error) {
	now := unixNanos(s.now())
	searchResult, err := s.db.ExecContext(ctx, "DELETE FROM search_entries WHERE expires_at <= ?", now)
	if err != nil {
		return false, fmt.Errorf("delete expired search entries: %w", err)
	}
	documentResult, err := s.db.ExecContext(ctx, "DELETE FROM documents WHERE expires_at <= ?", now)
	if err != nil {
		return false, fmt.Errorf("delete expired documents: %w", err)
	}
	searchDeleted, err := searchResult.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted search entries: %w", err)
	}
	documentsDeleted, err := documentResult.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted documents: %w", err)
	}
	return searchDeleted > 0 || documentsDeleted > 0, nil
}

func (s *Store) deleteOrphanedResults(ctx context.Context) (bool, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM search_results WHERE NOT EXISTS (SELECT 1 FROM search_entries WHERE id = search_results.search_id)")
	if err != nil {
		return false, fmt.Errorf("delete orphaned search results: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted orphaned search results: %w", err)
	}
	return deleted > 0, nil
}

func (s *Store) deleteLeastRecentlyUsedDocument(ctx context.Context) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM documents WHERE id = (
		SELECT id FROM documents ORDER BY last_accessed_at, id LIMIT 1
	)`)
	if err != nil {
		return false, fmt.Errorf("evict least recently used document: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count evicted documents: %w", err)
	}
	return deleted > 0, nil
}

func (s *Store) checkpoint(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("checkpoint cache database: %w", err)
	}
	return nil
}

func (s *Store) vacuum(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("compact cache database: %w", err)
	}
	return nil
}

func (s *Store) databaseHasFreePages(ctx context.Context) (bool, error) {
	var freePages int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&freePages); err != nil {
		return false, fmt.Errorf("read cache free pages: %w", err)
	}
	return freePages > 0, nil
}

func (s *Store) databaseSize(ctx context.Context) (int64, error) {
	var pageCount, pageSize int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, fmt.Errorf("read cache page count: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("read cache page size: %w", err)
	}
	return pageCount * pageSize, nil
}
