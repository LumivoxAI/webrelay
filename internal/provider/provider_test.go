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

func (s *ProviderSuite) manager(states map[Name]State, policies map[Name]Policy) *Manager {
	manager := NewManager(states, policies)
	manager.now = func() time.Time { return s.now }
	manager.sleep = func(_ context.Context, duration time.Duration) error {
		s.sleeps = append(s.sleeps, duration)
		return nil
	}
	return manager
}

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
		map[Name]State{EXA: STATE_AVAILABLE, BRAVE: STATE_AVAILABLE},
		map[Name]Policy{EXA: policy, BRAVE: policy},
	)
	calls := make(map[Name]int)

	result, err := manager.Route(context.Background(), []Name{EXA, BRAVE}, func(_ context.Context, name Name) error {
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
		map[Name]State{EXA: STATE_AVAILABLE, BRAVE: STATE_AVAILABLE},
		map[Name]Policy{EXA: policy, BRAVE: policy},
	)
	calls := make(map[Name]int)

	result, err := manager.Route(context.Background(), []Name{EXA, BRAVE}, func(_ context.Context, name Name) error {
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
		map[Name]State{EXA: STATE_AVAILABLE, BRAVE: STATE_AVAILABLE},
		map[Name]Policy{EXA: policy, BRAVE: policy},
	)

	_, err := manager.Route(context.Background(), []Name{EXA}, func(context.Context, Name) error {
		return &Failure{Reason: REASON_TEMPORARY, Retryable: true}
	})
	s.Require().Error(err)
	s.Equal(STATE_COOLDOWN, manager.State(EXA))

	calls := make(map[Name]int)
	result, err := manager.Route(context.Background(), []Name{EXA, BRAVE}, func(_ context.Context, name Name) error {
		calls[name]++
		return nil
	})
	s.Require().NoError(err)
	s.Equal(BRAVE, result.Provider)
	s.Equal([]Attempt{{Provider: EXA, Reason: REASON_TEMPORARY}}, result.Attempts)
	s.Zero(calls[EXA])

	s.now = s.now.Add(time.Minute)
	s.Equal(STATE_AVAILABLE, manager.State(EXA))
}

func (s *ProviderSuite) TestUnauthorizedMarksProviderMisconfigured() {
	policy := s.policy()
	manager := s.manager(map[Name]State{EXA: STATE_AVAILABLE}, map[Name]Policy{EXA: policy})

	_, err := manager.Route(context.Background(), []Name{EXA}, func(context.Context, Name) error {
		return &Failure{Reason: REASON_UNAUTHORIZED}
	})

	s.Require().Error(err)
	s.Equal(STATE_MISCONFIGURED, manager.State(EXA))
}

func (s *ProviderSuite) TestRateLimitAndQuotaUseDedicatedCooldowns() {
	policy := s.policy()
	manager := s.manager(map[Name]State{EXA: STATE_AVAILABLE, BRAVE: STATE_AVAILABLE}, map[Name]Policy{EXA: policy, BRAVE: policy})

	_, err := manager.Route(context.Background(), []Name{BRAVE}, func(context.Context, Name) error {
		return &Failure{Reason: REASON_RATE_LIMITED, Cooldown: 45 * time.Second}
	})
	s.Require().Error(err)
	s.Equal(STATE_COOLDOWN, manager.State(BRAVE))
	s.now = s.now.Add(45 * time.Second)
	s.Equal(STATE_AVAILABLE, manager.State(BRAVE))

	_, err = manager.Route(context.Background(), []Name{EXA}, func(context.Context, Name) error {
		return &Failure{Reason: REASON_QUOTA}
	})
	s.Require().Error(err)
	s.Equal(STATE_COOLDOWN, manager.State(EXA))
	s.now = s.now.Add(time.Hour)
	s.Equal(STATE_AVAILABLE, manager.State(EXA))
}

func (s *ProviderSuite) TestMixedFailuresReturnAggregateCode() {
	policy := s.policy()
	manager := s.manager(
		map[Name]State{EXA: STATE_AVAILABLE, BRAVE: STATE_AVAILABLE},
		map[Name]Policy{EXA: policy, BRAVE: policy},
	)

	_, err := manager.Route(context.Background(), []Name{EXA, BRAVE}, func(_ context.Context, name Name) error {
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

	manager := NewConfiguredManager(cfg)

	s.Equal(STATE_DISABLED, manager.State(EXA))
	s.Equal(STATE_AVAILABLE, manager.State(BRAVE))
	s.Equal(STATE_AVAILABLE, manager.State(MARKDOWN_NEW))
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
	exaTransport := clients[EXA].Transport.(*http.Transport)
	braveTransport := clients[BRAVE].Transport.(*http.Transport)
	markdownNewTransport := clients[MARKDOWN_NEW].Transport.(*http.Transport)
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
