package provider

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// MetricSnapshot is the in-memory diagnostic state for one provider action.
type MetricSnapshot struct {
	Requests        uint64
	Successes       uint64
	Failures        uint64
	FallbacksFrom   uint64
	FallbacksTo     uint64
	CacheHits       uint64
	TotalLatency    time.Duration
	RateLimitErrors uint64
	QuotaErrors     uint64
	CreditsUsed     float64
}

// Metrics records provider/action diagnostics without exposing them over HTTP.
type Metrics struct {
	mu      sync.Mutex
	entries map[Key]MetricSnapshot
	logger  *zap.Logger
}

func NewMetrics(logger *zap.Logger) *Metrics {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Metrics{entries: make(map[Key]MetricSnapshot), logger: logger}
}

func (m *Metrics) RecordAttempt(key Key, latency time.Duration, failure *Failure) {
	m.mu.Lock()
	snapshot := m.entries[key]
	snapshot.Requests++
	snapshot.TotalLatency += latency
	if failure == nil {
		snapshot.Successes++
	} else {
		snapshot.Failures++
		switch failure.Reason {
		case REASON_RATE_LIMITED:
			snapshot.RateLimitErrors++
		case REASON_QUOTA:
			snapshot.QuotaErrors++
		}
	}
	m.entries[key] = snapshot
	m.mu.Unlock()
	m.log(key, snapshot)
}

func (m *Metrics) RecordFallback(from, to Key) {
	m.mu.Lock()
	fromSnapshot := m.entries[from]
	fromSnapshot.FallbacksFrom++
	m.entries[from] = fromSnapshot
	toSnapshot := m.entries[to]
	toSnapshot.FallbacksTo++
	m.entries[to] = toSnapshot
	m.mu.Unlock()
	m.log(from, fromSnapshot)
	m.log(to, toSnapshot)
}

// RecordCacheHit records a cache state reported by an upstream provider.
func (m *Metrics) RecordCacheHit(key Key) {
	m.mu.Lock()
	snapshot := m.entries[key]
	snapshot.CacheHits++
	m.entries[key] = snapshot
	m.mu.Unlock()
	m.log(key, snapshot)
}

// RecordCredits adds upstream-reported credits for one provider action.
func (m *Metrics) RecordCredits(key Key, credits float64) {
	if credits <= 0 {
		return
	}
	m.mu.Lock()
	snapshot := m.entries[key]
	snapshot.CreditsUsed += credits
	m.entries[key] = snapshot
	m.mu.Unlock()
	m.log(key, snapshot)
}

func (m *Metrics) Snapshot(key Key) MetricSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entries[key]
}

func (m *Metrics) log(key Key, snapshot MetricSnapshot) {
	m.logger.Debug("Provider action metrics",
		zap.String("provider", string(key.Provider)),
		zap.String("action", string(key.Action)),
		zap.Uint64("requests", snapshot.Requests),
		zap.Uint64("successes", snapshot.Successes),
		zap.Uint64("failures", snapshot.Failures),
		zap.Uint64("fallbacks_from", snapshot.FallbacksFrom),
		zap.Uint64("fallbacks_to", snapshot.FallbacksTo),
		zap.Uint64("cache_hits", snapshot.CacheHits),
		zap.Int64("total_latency_ms", snapshot.TotalLatency.Milliseconds()),
		zap.Uint64("rate_limit_errors", snapshot.RateLimitErrors),
		zap.Uint64("quota_errors", snapshot.QuotaErrors),
		zap.Float64("credits_used", snapshot.CreditsUsed),
	)
}
