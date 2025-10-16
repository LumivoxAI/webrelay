package markdownnew

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"
)

type ClientSuite struct {
	suite.Suite
	settings config.MarkdownNewConfig
}

func (s *ClientSuite) SetupTest() {
	s.settings = config.DefaultMarkdownNewConfig()
}

func (s *ClientSuite) client(server *httptest.Server) *Client {
	client, err := NewAtURL(server.URL, s.settings, server.Client(), zap.NewNop())
	s.Require().NoError(err)
	return client
}

func (s *ClientSuite) TestFetchSendsExpectedPayload() {
	var received requestPayload
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.Equal(http.MethodPost, request.Method)
		s.Equal("application/json", request.Header.Get("Content-Type"))
		s.Contains(request.Header.Get("Accept"), "text/markdown")
		s.Require().NoError(json.NewDecoder(request.Body).Decode(&received))
		writer.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = writer.Write([]byte("# Document\n\nContent"))
	}))
	defer server.Close()

	response, err := s.client(server).Fetch(context.Background(), ContentRequest{URL: "https://example.com/document"})

	s.Require().NoError(err)
	s.Equal("https://example.com/document", received.URL)
	s.Equal("auto", received.Method)
	s.False(received.RetainImages)
	s.Equal("# Document\n\nContent", response.Content)
	s.Require().NotNil(response.SourceMediaType)
	s.Equal("text/markdown", *response.SourceMediaType)
}

func (s *ClientSuite) TestFetchRejectsUnusableContent() {
	for _, content := range []string{
		"",
		" \n\t ",
		"<!DOCTYPE html><html><body>Error</body></html>",
		"Access denied",
		"Too many requests",
	} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(content))
		}))

		_, err := s.client(server).Fetch(context.Background(), ContentRequest{URL: "https://example.com"})
		server.Close()

		var failure *provider.Failure
		s.Require().True(errors.As(err, &failure), "content %q", content)
		s.Equal(provider.REASON_UNAVAILABLE, failure.Reason, "content %q", content)
	}
}

func (s *ClientSuite) TestFetchRejectsUnsupportedResponseFormat() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"error":"not available"}`))
	}))
	defer server.Close()

	_, err := s.client(server).Fetch(context.Background(), ContentRequest{URL: "https://example.com"})

	var failure *provider.Failure
	s.Require().True(errors.As(err, &failure))
	s.Equal(provider.REASON_UNAVAILABLE, failure.Reason)
}

func (s *ClientSuite) TestFetchAcceptsShortMarkdownWithoutErrorMarker() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("# Note\n\nBrief documentation."))
	}))
	defer server.Close()

	response, err := s.client(server).Fetch(context.Background(), ContentRequest{URL: "https://example.com"})

	s.Require().NoError(err)
	s.Equal("# Note\n\nBrief documentation.", response.Content)
}

func (s *ClientSuite) TestFetchAppliesCooldownWhenQuotaHeaderIsZero() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("x-rate-limit-remaining", "0")
		_, _ = writer.Write([]byte("# Document\n\nContent"))
	}))
	defer server.Close()

	_, err := s.client(server).Fetch(context.Background(), ContentRequest{URL: "https://example.com"})

	var failure *provider.Failure
	s.Require().True(errors.As(err, &failure))
	s.Equal(provider.REASON_RATE_LIMITED, failure.Reason)
	s.True(failure.Retryable)
	s.Equal(s.settings.RateLimitCooldown.Std(), failure.Cooldown)
}

func (s *ClientSuite) TestHTTPFailuresAreClassified() {
	tests := []struct {
		status    int
		reason    provider.Reason
		retryable bool
	}{
		{status: http.StatusUnauthorized, reason: provider.REASON_UNAUTHORIZED},
		{status: http.StatusForbidden, reason: provider.REASON_FORBIDDEN},
		{status: http.StatusTooManyRequests, reason: provider.REASON_RATE_LIMITED, retryable: true},
		{status: http.StatusServiceUnavailable, reason: provider.REASON_TEMPORARY, retryable: true},
		{status: http.StatusUnprocessableEntity, reason: provider.REASON_UNAVAILABLE},
	}
	for _, test := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(test.status)
		}))

		_, err := s.client(server).Fetch(context.Background(), ContentRequest{URL: "https://example.com"})
		server.Close()

		var failure *provider.Failure
		s.Require().True(errors.As(err, &failure), "status %d", test.status)
		s.Equal(test.reason, failure.Reason, "status %d", test.status)
		s.Equal(test.retryable, failure.Retryable, "status %d", test.status)
		if test.status == http.StatusTooManyRequests {
			s.Equal(s.settings.RateLimitCooldown.Std(), failure.Cooldown)
		}
	}
}

func (s *ClientSuite) TestTimeoutIsRetryable() {
	client, err := NewAtURL(DEFAULT_BASE_URL, s.settings, &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		}),
	}, zap.NewNop())
	s.Require().NoError(err)

	_, err = client.Fetch(context.Background(), ContentRequest{URL: "https://example.com"})

	var failure *provider.Failure
	s.Require().True(errors.As(err, &failure))
	s.Equal(provider.REASON_TIMEOUT, failure.Reason)
	s.True(failure.Retryable)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}
