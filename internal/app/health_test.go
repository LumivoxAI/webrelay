package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/LumivoxAI/webrelay/internal/cache"
	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type HealthSuite struct {
	suite.Suite
	store   *cache.Store
	checker *ReadinessChecker
}

func TestHealthSuite(t *testing.T) {
	suite.Run(t, new(HealthSuite))
}

func (s *HealthSuite) SetupTest() {
	cfg := config.Default()
	cfg.Cache.Path = filepath.Join(s.T().TempDir(), "cache.db")
	store, err := cache.Open(context.Background(), cfg.Cache)
	require.NoError(s.T(), err)
	s.store = store
	manager := provider.NewManager(map[provider.Key]provider.State{
		{Provider: provider.EXA, Action: provider.SEARCH}:   provider.STATE_AVAILABLE,
		{Provider: provider.EXA, Action: provider.CONTENTS}: provider.STATE_AVAILABLE,
	}, map[provider.Key]provider.Policy{})
	s.checker = NewReadinessChecker(cfg, store, manager)
}

func (s *HealthSuite) TearDownTest() {
	if s.store != nil {
		_ = s.store.Close()
	}
}

func (s *HealthSuite) TestReadyUsesSQLiteAndActionStates() {
	response, ready := s.checker.Ready(context.Background())

	s.True(ready)
	s.Equal("ready", response.Status)
	s.Equal("available", response.SearchProviders["exa"])
	s.Equal("available", response.ContentProviders["exa"])
	s.Equal("disabled", response.SearchProviders["brave"])
}

func (s *HealthSuite) TestReadyFailsWhenSQLiteIsClosed() {
	s.Require().NoError(s.store.Close())

	response, ready := s.checker.Ready(context.Background())

	s.False(ready)
	s.Equal("not_ready", response.Status)
	s.store = nil
}
