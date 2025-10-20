package provider

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/stretchr/testify/suite"
)

type ProviderSuite struct {
	suite.Suite
	now    time.Time
	sleeps []time.Duration
}

func (s *ProviderSuite) SetupTest() {
	s.now = time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	s.sleeps = nil
}

func (s *ProviderSuite) manager(states map[Key]State, policies map[Key]Policy) *Manager {
	manager := NewManager(states, policies)
	manager.now = func() time.Time { return s.now }
	manager.sleep = func(_ context.Context, duration time.Duration) error {
		s.sleeps = append(s.sleeps, duration)
		return nil
	}
	return manager
}

func key(name Name, action Action) Key { return Key{Provider: name, Action: action} }

func (s *ProviderSuite) policy() Policy {
	return Policy{
		MaxAttempts:       3,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        250 * time.Millisecond,
		FailureThreshold:  3,
		Cooldown:          time.Minute,
		QuotaCooldown:     time.Hour,
		RateLimitCooldown: 30 * time.Minute,
	}
}

func (s *ProviderSuite) TestRetriesThenSucceedsWithoutFallback() {
	policy := s.policy()
	manager := s.manager(
		map[Key]State{key(EXA, SEARCH): STATE_AVAILABLE, key(BRAVE, SEARCH): STATE_AVAILABLE},
		map[Key]Policy{key(EXA, SEARCH): policy, key(BRAVE, SEARCH): policy},
	)
	calls := make(map[Name]int)

	result, err := manager.Route(context.Background(), SEARCH, []Name{EXA, BRAVE}, func(_ context.Context, name Name) error {
		calls[name]++
		if name == EXA && calls[name] < 3 {
			return &Failure{Reason: REASON_TEMPORARY, Retryable: true}
		}
		return nil
	})

	s.Require().NoError(err)
	s.Equal(EXA, result.Provider)
	s.Empty(result.Attempts)
	s.Equal(3, calls[EXA])
	s.Zero(calls[BRAVE])
	s.Equal([]time.Duration{100 * time.Millisecond, 200 * time.Millisecond}, s.sleeps)
}

func (s *ProviderSuite) TestNonRetryableFailureFallsBack() {
	policy := s.policy()
	manager := s.manager(
		map[Key]State{key(EXA, SEARCH): STATE_AVAILABLE, key(BRAVE, SEARCH): STATE_AVAILABLE},
		map[Key]Policy{key(EXA, SEARCH): policy, key(BRAVE, SEARCH): policy},
	)
	calls := make(map[Name]int)

	result, err := manager.Route(context.Background(), SEARCH, []Name{EXA, BRAVE}, func(_ context.Context, name Name) error {
		calls[name]++
		if name == EXA {
			return &Failure{Reason: REASON_FORBIDDEN}
		}
		return nil
	})

	s.Require().NoError(err)
	s.Equal(BRAVE, result.Provider)
	s.Equal([]Attempt{{Provider: EXA, Reason: REASON_FORBIDDEN}}, result.Attempts)
	s.Equal(1, calls[EXA])
	s.Equal(1, calls[BRAVE])
	s.Empty(s.sleeps)
}

func (s *ProviderSuite) TestCooldownSkipsProviderAndExpires() {
	policy := s.policy()
	policy.FailureThreshold = 1
	manager := s.manager(
		map[Key]State{key(EXA, SEARCH): STATE_AVAILABLE, key(BRAVE, SEARCH): STATE_AVAILABLE},
		map[Key]Policy{key(EXA, SEARCH): policy, key(BRAVE, SEARCH): policy},
	)

	_, err := manager.Route(context.Background(), SEARCH, []Name{EXA}, func(context.Context, Name) error {
		return &Failure{Reason: REASON_TEMPORARY, Retryable: true}
	})
	s.Require().Error(err)
	s.Equal(STATE_COOLDOWN, manager.State(key(EXA, SEARCH)))

	calls := make(map[Name]int)
	result, err := manager.Route(context.Background(), SEARCH, []Name{EXA, BRAVE}, func(_ context.Context, name Name) error {
		calls[name]++
		return nil
	})
	s.Require().NoError(err)
	s.Equal(BRAVE, result.Provider)
	s.Equal([]Attempt{{Provider: EXA, Reason: REASON_TEMPORARY}}, result.Attempts)
	s.Zero(calls[EXA])

	s.now = s.now.Add(time.Minute)
	s.Equal(STATE_AVAILABLE, manager.State(key(EXA, SEARCH)))
}

func (s *ProviderSuite) TestUnauthorizedMarksProviderMisconfigured() {
	policy := s.policy()
	manager := s.manager(map[Key]State{key(EXA, SEARCH): STATE_AVAILABLE}, map[Key]Policy{key(EXA, SEARCH): policy})

	_, err := manager.Route(context.Background(), SEARCH, []Name{EXA}, func(context.Context, Name) error {
		return &Failure{Reason: REASON_UNAUTHORIZED}
	})

	s.Require().Error(err)
	s.Equal(STATE_MISCONFIGURED, manager.State(key(EXA, SEARCH)))
}

func (s *ProviderSuite) TestRateLimitAndQuotaUseDedicatedCooldowns() {
	policy := s.policy()
	manager := s.manager(map[Key]State{key(EXA, CONTENTS): STATE_AVAILABLE, key(BRAVE, SEARCH): STATE_AVAILABLE}, map[Key]Policy{key(EXA, CONTENTS): policy, key(BRAVE, SEARCH): policy})

	_, err := manager.Route(context.Background(), SEARCH, []Name{BRAVE}, func(context.Context, Name) error {
		return &Failure{Reason: REASON_RATE_LIMITED, Cooldown: 45 * time.Second}
	})
	s.Require().Error(err)
	s.Equal(STATE_COOLDOWN, manager.State(key(BRAVE, SEARCH)))
	s.now = s.now.Add(45 * time.Second)
	s.Equal(STATE_AVAILABLE, manager.State(key(BRAVE, SEARCH)))

	_, err = manager.Route(context.Background(), CONTENTS, []Name{EXA}, func(context.Context, Name) error {
		return &Failure{Reason: REASON_QUOTA}
	})
	s.Require().Error(err)
	s.Equal(STATE_COOLDOWN, manager.State(key(EXA, CONTENTS)))
	s.now = s.now.Add(time.Hour)
	s.Equal(STATE_AVAILABLE, manager.State(key(EXA, CONTENTS)))
}

func (s *ProviderSuite) TestMixedFailuresReturnAggregateCode() {
	policy := s.policy()
	manager := s.manager(
		map[Key]State{key(EXA, SEARCH): STATE_AVAILABLE, key(BRAVE, SEARCH): STATE_AVAILABLE},
		map[Key]Policy{key(EXA, SEARCH): policy, key(BRAVE, SEARCH): policy},
	)

	_, err := manager.Route(context.Background(), SEARCH, []Name{EXA, BRAVE}, func(_ context.Context, name Name) error {
		if name == EXA {
			return &Failure{Reason: REASON_TIMEOUT}
		}
		return &Failure{Reason: REASON_QUOTA}
	})

	var routeError *RouteError
	s.Require().True(errors.As(err, &routeError))
	s.Equal(Reason("all_providers_failed"), routeError.Code())
	s.Equal([]Attempt{{Provider: EXA, Reason: REASON_TIMEOUT}, {Provider: BRAVE, Reason: REASON_QUOTA}}, routeError.Attempts)
}

func (s *ProviderSuite) TestConfiguredManagerUsesInitialProviderStates() {
	cfg := config.Default()
	cfg.Providers.Exa.Enabled = false
	cfg.Providers.Brave.APIKey = "brave-secret"
	s.Require().NoError(cfg.Validate())

	manager := NewConfiguredManager(cfg, nil)

	s.Equal(STATE_DISABLED, manager.State(key(EXA, SEARCH)))
	s.Equal(STATE_AVAILABLE, manager.State(key(BRAVE, SEARCH)))
	s.Equal(STATE_AVAILABLE, manager.State(key(MARKDOWN_NEW, FETCH)))
}

func (s *ProviderSuite) TestActionFailuresDoNotAffectOtherActions() {
	policy := s.policy()
	policy.FailureThreshold = 1
	search := key(EXA, SEARCH)
	contents := key(EXA, CONTENTS)
	manager := s.manager(
		map[Key]State{search: STATE_AVAILABLE, contents: STATE_AVAILABLE},
		map[Key]Policy{search: policy, contents: policy},
	)

	_, err := manager.Route(context.Background(), CONTENTS, []Name{EXA}, func(context.Context, Name) error {
		return &Failure{Reason: REASON_QUOTA}
	})

	s.Require().Error(err)
	s.Equal(STATE_COOLDOWN, manager.State(contents))
	s.Equal(STATE_AVAILABLE, manager.State(search))
}

func (s *ProviderSuite) TestMetricsRecordAttemptsFallbacksAndCredits() {
	policy := s.policy()
	exa := key(EXA, SEARCH)
	brave := key(BRAVE, SEARCH)
	metrics := NewMetrics(nil)
	manager := NewManagerWithMetrics(
		map[Key]State{exa: STATE_AVAILABLE, brave: STATE_AVAILABLE},
		map[Key]Policy{exa: policy, brave: policy},
		metrics,
	)
	manager.now = func() time.Time { return s.now }

	_, err := manager.Route(context.Background(), SEARCH, []Name{EXA, BRAVE}, func(_ context.Context, name Name) error {
		if name == EXA {
			return &Failure{Reason: REASON_RATE_LIMITED}
		}
		return nil
	})
	metrics.RecordCacheHit(brave)
	metrics.RecordCredits(brave, 2.5)

	s.Require().NoError(err)
	s.Equal(uint64(1), metrics.Snapshot(exa).Requests)
	s.Equal(uint64(1), metrics.Snapshot(exa).Failures)
	s.Equal(uint64(1), metrics.Snapshot(exa).RateLimitErrors)
	s.Equal(uint64(1), metrics.Snapshot(exa).FallbacksFrom)
	s.Equal(uint64(1), metrics.Snapshot(brave).Successes)
	s.Equal(uint64(1), metrics.Snapshot(brave).FallbacksTo)
	s.Equal(uint64(1), metrics.Snapshot(brave).CacheHits)
	s.Equal(2.5, metrics.Snapshot(brave).CreditsUsed)
}

func (s *ProviderSuite) TestClientsUseSeparateTransportsAndSupportedProxies() {
	direct, err := NewHTTPClient(time.Second, "")
	s.Require().NoError(err)
	socks, err := NewHTTPClient(time.Second, "socks5://127.0.0.1:1080")
	s.Require().NoError(err)
	httpsProxy, err := NewHTTPClient(time.Second, "https://proxy.example:8443")
	s.Require().NoError(err)

	directTransport := direct.Transport.(*http.Transport)
	socksTransport := socks.Transport.(*http.Transport)
	httpsTransport := httpsProxy.Transport.(*http.Transport)
	s.NotSame(directTransport, socksTransport)
	s.NotNil(socksTransport.DialContext)
	proxyURL, err := httpsTransport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "api.example"}})
	s.Require().NoError(err)
	s.Equal("https://proxy.example:8443", proxyURL.String())
}

func (s *ProviderSuite) TestConfiguredClientsUseCommonProxy() {
	cfg := config.Default()
	cfg.Proxy.URL = "https://proxy.example:8443"

	clients, err := NewConfiguredClients(cfg)

	s.Require().NoError(err)
	exaTransport := clients[key(EXA, SEARCH)].Transport.(*http.Transport)
	braveTransport := clients[key(BRAVE, SEARCH)].Transport.(*http.Transport)
	markdownNewTransport := clients[key(MARKDOWN_NEW, FETCH)].Transport.(*http.Transport)
	s.NotSame(exaTransport, braveTransport)
	s.NotSame(braveTransport, markdownNewTransport)
	for _, transport := range []*http.Transport{exaTransport, braveTransport, markdownNewTransport} {
		proxyURL, proxyErr := transport.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "api.example"}})
		s.Require().NoError(proxyErr)
		s.Equal("https://proxy.example:8443", proxyURL.String())
	}
}

func TestProviderSuite(t *testing.T) {
	suite.Run(t, new(ProviderSuite))
}
