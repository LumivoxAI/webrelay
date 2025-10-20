package provider

import (
	"context"
	"errors"
	"sync"
	"time"
)

type entry struct {
	state         State
	failures      int
	cooldownUntil time.Time
	lastReason    Reason
	policy        Policy
}

// Manager holds independent mutable state for each provider.
type Manager struct {
	mu      sync.Mutex
	entries map[Key]*entry
	metrics *Metrics
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
}

// NewManager creates a manager with the supplied initial state and policies.
func NewManager(initial map[Key]State, policies map[Key]Policy) *Manager {
	return NewManagerWithMetrics(initial, policies, NewMetrics(nil))
}

// NewManagerWithMetrics creates a manager with an explicit diagnostics registry.
func NewManagerWithMetrics(initial map[Key]State, policies map[Key]Policy, metrics *Metrics) *Manager {
	entries := make(map[Key]*entry, len(initial))
	for key, state := range initial {
		entry := &entry{state: state, policy: policies[key]}
		if state != STATE_AVAILABLE {
			entry.lastReason = REASON_MISCONFIGURED
		}
		entries[key] = entry
	}
	if metrics == nil {
		metrics = NewMetrics(nil)
	}
	return &Manager{
		entries: entries,
		metrics: metrics,
		now:     time.Now,
		sleep:   sleep,
	}
}

// Metrics returns the manager's action-scoped diagnostics registry.
func (m *Manager) Metrics() *Metrics { return m.metrics }

// Policy is the retry and cooldown configuration for one provider.
type Policy struct {
	MaxAttempts       int
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	FailureThreshold  int
	Cooldown          time.Duration
	QuotaCooldown     time.Duration
	RateLimitCooldown time.Duration
}

// State returns a provider's current state, expiring elapsed cooldowns.
func (m *Manager) State(key Key) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[key]
	if !ok {
		return STATE_DISABLED
	}
	m.refresh(entry)
	return entry.state
}

// Route invokes providers in order until one operation succeeds.
func (m *Manager) Route(ctx context.Context, action Action, providers []Name, operation Operation) (Result, error) {
	attempts := make([]Attempt, 0, len(providers))
	var fallbackFrom *Key
	for _, name := range providers {
		key := Key{Provider: name, Action: action}
		entry, state := m.get(key)
		if entry == nil {
			attempts = append(attempts, Attempt{Provider: name, Reason: REASON_MISCONFIGURED})
			continue
		}
		if state != STATE_AVAILABLE {
			attempts = append(attempts, Attempt{Provider: name, Reason: entry.lastReason})
			continue
		}
		if fallbackFrom != nil {
			m.metrics.RecordFallback(*fallbackFrom, key)
			fallbackFrom = nil
		}

		failure := m.call(ctx, key, entry, operation)
		if failure == nil {
			return Result{Provider: name, Attempts: attempts}, nil
		}
		attempts = append(attempts, Attempt{Provider: name, Reason: failure.Reason})
		failedKey := key
		fallbackFrom = &failedKey
	}
	return Result{}, &RouteError{Attempts: attempts}
}

func (m *Manager) get(key Key) (*entry, State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[key]
	if entry == nil {
		return nil, STATE_DISABLED
	}
	m.refresh(entry)
	return entry, entry.state
}

func (m *Manager) call(ctx context.Context, key Key, entry *entry, operation Operation) *Failure {
	for attempt := 1; attempt <= entry.policy.MaxAttempts; attempt++ {
		startedAt := m.now()
		err := operation(ctx, key.Provider)
		latency := m.now().Sub(startedAt)
		if err == nil {
			m.succeed(entry)
			m.metrics.RecordAttempt(key, latency, nil)
			return nil
		}
		failure := classify(err)
		m.metrics.RecordAttempt(key, latency, failure)
		m.fail(entry, failure)
		if !failure.Retryable || entry.policy.MaxAttempts == attempt || m.State(key) != STATE_AVAILABLE {
			return failure
		}
		if err := m.sleep(ctx, backoff(entry.policy, attempt)); err != nil {
			return &Failure{Reason: REASON_TIMEOUT, Cause: err}
		}
	}
	return &Failure{Reason: REASON_TEMPORARY}
}

func (m *Manager) succeed(entry *entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.failures = 0
	entry.lastReason = ""
}

func (m *Manager) fail(entry *entry, failure *Failure) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.lastReason = failure.Reason
	switch failure.Reason {
	case REASON_UNAUTHORIZED, REASON_MISCONFIGURED:
		entry.state = STATE_MISCONFIGURED
	case REASON_QUOTA:
		m.cooldown(entry, entry.policy.QuotaCooldown)
	case REASON_RATE_LIMITED:
		duration := failure.Cooldown
		if duration <= 0 {
			duration = entry.policy.RateLimitCooldown
		}
		m.cooldown(entry, duration)
	default:
		if failure.Retryable {
			entry.failures++
			if entry.failures >= entry.policy.FailureThreshold {
				m.cooldown(entry, entry.policy.Cooldown)
			}
		}
	}
}

func (m *Manager) cooldown(entry *entry, duration time.Duration) {
	entry.state = STATE_COOLDOWN
	entry.cooldownUntil = m.now().Add(duration)
	entry.failures = 0
}

func (m *Manager) refresh(entry *entry) {
	if entry.state == STATE_COOLDOWN && !m.now().Before(entry.cooldownUntil) {
		entry.state = STATE_AVAILABLE
		entry.cooldownUntil = time.Time{}
		entry.lastReason = ""
	}
}

func classify(err error) *Failure {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure
	}
	return &Failure{Reason: REASON_TEMPORARY, Retryable: true, Cause: err}
}

func backoff(policy Policy, retry int) time.Duration {
	duration := policy.InitialBackoff
	for index := 1; index < retry && duration < policy.MaxBackoff; index++ {
		duration *= 2
	}
	if duration > policy.MaxBackoff {
		return policy.MaxBackoff
	}
	return duration
}

func sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
