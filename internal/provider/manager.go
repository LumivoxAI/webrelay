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
	entries map[Name]*entry
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
}

// NewManager creates a manager with the supplied initial state and policies.
func NewManager(initial map[Name]State, policies map[Name]Policy) *Manager {
	entries := make(map[Name]*entry, len(initial))
	for name, state := range initial {
		entry := &entry{state: state, policy: policies[name]}
		if state != StateAvailable {
			entry.lastReason = ReasonMisconfigured
		}
		entries[name] = entry
	}
	return &Manager{
		entries: entries,
		now:     time.Now,
		sleep:   sleep,
	}
}

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
func (m *Manager) State(name Name) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[name]
	if !ok {
		return StateDisabled
	}
	m.refresh(entry)
	return entry.state
}

// Route invokes providers in order until one operation succeeds.
func (m *Manager) Route(ctx context.Context, providers []Name, operation Operation) (Result, error) {
	attempts := make([]Attempt, 0, len(providers))
	for _, name := range providers {
		entry, state := m.get(name)
		if entry == nil {
			attempts = append(attempts, Attempt{Provider: name, Reason: ReasonMisconfigured})
			continue
		}
		if state != StateAvailable {
			attempts = append(attempts, Attempt{Provider: name, Reason: entry.lastReason})
			continue
		}

		failure := m.call(ctx, name, entry, operation)
		if failure == nil {
			return Result{Provider: name, Attempts: attempts}, nil
		}
		attempts = append(attempts, Attempt{Provider: name, Reason: failure.Reason})
	}
	return Result{}, &RouteError{Attempts: attempts}
}

func (m *Manager) get(name Name) (*entry, State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[name]
	if entry == nil {
		return nil, StateDisabled
	}
	m.refresh(entry)
	return entry, entry.state
}

func (m *Manager) call(ctx context.Context, name Name, entry *entry, operation Operation) *Failure {
	for attempt := 1; attempt <= entry.policy.MaxAttempts; attempt++ {
		err := operation(ctx, name)
		if err == nil {
			m.succeed(entry)
			return nil
		}
		failure := classify(err)
		m.fail(entry, failure)
		if !failure.Retryable || entry.policy.MaxAttempts == attempt || m.State(name) != StateAvailable {
			return failure
		}
		if err := m.sleep(ctx, backoff(entry.policy, attempt)); err != nil {
			return &Failure{Reason: ReasonTimeout, Cause: err}
		}
	}
	return &Failure{Reason: ReasonTemporary}
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
	case ReasonUnauthorized, ReasonMisconfigured:
		entry.state = StateMisconfigured
	case ReasonQuota:
		m.cooldown(entry, entry.policy.QuotaCooldown)
	case ReasonRateLimited:
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
	entry.state = StateCooldown
	entry.cooldownUntil = m.now().Add(duration)
	entry.failures = 0
}

func (m *Manager) refresh(entry *entry) {
	if entry.state == StateCooldown && !m.now().Before(entry.cooldownUntil) {
		entry.state = StateAvailable
		entry.cooldownUntil = time.Time{}
		entry.lastReason = ""
	}
}

func classify(err error) *Failure {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure
	}
	return &Failure{Reason: ReasonTemporary, Retryable: true, Cause: err}
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
