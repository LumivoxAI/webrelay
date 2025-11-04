package app

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/LumivoxAI/webrelay/internal/cache"
	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

type LifecycleSuite struct {
	suite.Suite
}

func TestLifecycleSuite(t *testing.T) {
	suite.Run(t, new(LifecycleSuite))
}

func (s *LifecycleSuite) TestShutdownWaitsForRequestStopsCleanupAndClosesCache() {
	cfg := config.Default()
	cfg.Cache.Path = filepath.Join(s.T().TempDir(), "cache.db")
	store, err := cache.Open(context.Background(), cfg.Cache)
	require.NoError(s.T(), err)

	cleanupContext, cancelCleanup := context.WithCancel(context.Background())
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		store.StartCleanupWorker(cleanupContext, time.Hour, zap.NewNop())
	}()

	started := make(chan struct{})
	release := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(s.T(), err)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()

	requestDone := make(chan struct{})
	go func() {
		_, _ = http.Get("http://" + listener.Addr().String())
		close(requestDone)
	}()
	<-started

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		shutdownDone <- Shutdown(ctx, server, cancelCleanup, cleanupDone, store)
	}()
	select {
	case err := <-shutdownDone:
		s.Fail("shutdown finished before active request", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)

	s.Require().NoError(<-shutdownDone)
	<-requestDone
	s.Error(<-serveDone)
	s.Error(store.Ping(context.Background()))
}

func (s *LifecycleSuite) TestRecognizesLoopbackAddresses() {
	s.True(IsLoopbackListenAddress("127.0.0.1:8090"))
	s.True(IsLoopbackListenAddress("[::1]:8090"))
	s.True(IsLoopbackListenAddress("localhost:8090"))
	s.False(IsLoopbackListenAddress("0.0.0.0:8090"))
	s.False(IsLoopbackListenAddress("example.com:8090"))
}
