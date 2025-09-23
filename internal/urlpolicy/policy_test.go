package urlpolicy

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/suite"
)

type fakeResolver struct {
	addresses map[string][]netip.Addr
	err       error
}

func (r fakeResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.addresses[host], nil
}

type PolicySuite struct {
	suite.Suite
	policy Policy
}

func (s *PolicySuite) SetupTest() {
	s.policy = New(fakeResolver{addresses: map[string][]netip.Addr{
		"example.com":   {netip.MustParseAddr("93.184.216.34")},
		"go.dev":        {netip.MustParseAddr("216.239.32.21")},
		"github.com":    {netip.MustParseAddr("140.82.112.3")},
		"mixed.example": {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")},
	}})
}

func (s *PolicySuite) TestValidateAcceptsPublicURLsAndNormalizesThem() {
	testCases := []struct {
		rawURL      string
		expectedURL string
	}{
		{rawURL: "https://go.dev/doc/", expectedURL: "https://go.dev/doc/"},
		{rawURL: "https://github.com/golang/go", expectedURL: "https://github.com/golang/go"},
		{rawURL: "https://example.com/page?id=123&utm_source=campaign#section", expectedURL: "https://example.com/page?id=123"},
	}

	for _, testCase := range testCases {
		s.Run(testCase.rawURL, func() {
			url, err := s.policy.Validate(context.Background(), testCase.rawURL)

			s.Require().NoError(err)
			s.Equal(testCase.expectedURL, url.String())
		})
	}
}

func (s *PolicySuite) TestValidateRejectsUnsafeURLs() {
	testCases := []struct {
		name   string
		rawURL string
		err    error
	}{
		{name: "file scheme", rawURL: "file:///etc/passwd", err: ErrUnsupportedURL},
		{name: "localhost", rawURL: "http://localhost/", err: ErrUnsupportedURL},
		{name: "loopback IPv4", rawURL: "http://127.0.0.1/", err: ErrUnsupportedURL},
		{name: "private IPv4", rawURL: "http://10.0.0.1/", err: ErrUnsupportedURL},
		{name: "private IPv4 range", rawURL: "http://192.168.1.1/", err: ErrUnsupportedURL},
		{name: "link local IPv4", rawURL: "http://169.254.169.254/", err: ErrUnsupportedURL},
		{name: "loopback IPv6", rawURL: "http://[::1]/", err: ErrUnsupportedURL},
		{name: "local hostname", rawURL: "http://host.local/", err: ErrUnsupportedURL},
		{name: "credentials", rawURL: "https://user:password@example.com/", err: ErrUnsupportedURL},
		{name: "sensitive parameter", rawURL: "https://example.com/?access_token=secret", err: ErrUnsupportedURL},
		{name: "missing host", rawURL: "https:///path", err: ErrInvalidURL},
		{name: "invalid port", rawURL: "https://example.com:invalid/", err: ErrInvalidURL},
	}

	for _, testCase := range testCases {
		s.Run(testCase.name, func() {
			_, err := s.policy.Validate(context.Background(), testCase.rawURL)

			s.ErrorIs(err, testCase.err)
		})
	}
}

func (s *PolicySuite) TestValidateRejectsEmptyMixedAndFailedDNSAnswers() {
	for name, resolver := range map[string]fakeResolver{
		"empty answer": {addresses: map[string][]netip.Addr{}},
		"mixed answer": {addresses: map[string][]netip.Addr{
			"mixed.example": {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")},
		}},
		"resolver error": {err: errors.New("DNS unavailable")},
	} {
		s.Run(name, func() {
			policy := New(resolver)
			target := "https://example.com/"
			if name == "mixed answer" {
				target = "https://mixed.example/"
			}

			_, err := policy.Validate(context.Background(), target)

			s.ErrorIs(err, ErrUnsupportedURL)
		})
	}
}

func (s *PolicySuite) TestNormalizeDecodesAndComparesParameterNamesWithoutCase() {
	_, err := s.policy.Normalize("https://example.com/?%41CCESS_TOKEN=secret")

	s.ErrorIs(err, ErrUnsupportedURL)
}

func TestPolicySuite(t *testing.T) {
	suite.Run(t, new(PolicySuite))
}
