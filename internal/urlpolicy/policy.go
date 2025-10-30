// Package urlpolicy validates public URLs before they are sent to content providers.
package urlpolicy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

var (
	// ErrInvalidURL identifies a malformed URL that cannot be processed.
	ErrInvalidURL = errors.New("invalid URL")
	// ErrUnsupportedURL identifies a syntactically valid URL that is unsafe to fetch.
	ErrUnsupportedURL = errors.New("unsupported URL")
)

var trackingParameters = map[string]struct{}{
	"utm_source":   {},
	"utm_medium":   {},
	"utm_campaign": {},
	"utm_term":     {},
	"utm_content":  {},
	"gclid":        {},
	"fbclid":       {},
}

var sensitiveParameters = map[string]struct{}{
	"token":            {},
	"access_token":     {},
	"auth":             {},
	"authorization":    {},
	"api_key":          {},
	"apikey":           {},
	"key":              {},
	"signature":        {},
	"sig":              {},
	"x-amz-signature":  {},
	"x-goog-signature": {},
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// Resolver resolves a hostname to IP addresses. It is injected so URL checks remain deterministic in tests.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Policy validates and normalizes URLs for an external content provider.
type Policy struct {
	resolver Resolver
}

// New creates a URL policy. A nil resolver uses the system DNS resolver.
func New(resolver Resolver) Policy {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return Policy{resolver: resolver}
}

// Normalize removes fragments and known tracking parameters while rejecting malformed or unsafe URL syntax.
func (p Policy) Normalize(rawURL string) (*url.URL, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed syntax", ErrInvalidURL)
	}
	if !strings.EqualFold(parsedURL.Scheme, "http") && !strings.EqualFold(parsedURL.Scheme, "https") {
		return nil, fmt.Errorf("%w: scheme is not HTTP or HTTPS", ErrUnsupportedURL)
	}
	if parsedURL.Host == "" || parsedURL.Opaque != "" {
		return nil, fmt.Errorf("%w: host is required", ErrInvalidURL)
	}
	if parsedURL.User != nil {
		return nil, fmt.Errorf("%w: credentials are not allowed", ErrUnsupportedURL)
	}
	if !hasValidPort(parsedURL) {
		return nil, fmt.Errorf("%w: malformed host", ErrInvalidURL)
	}

	host := strings.TrimSuffix(strings.ToLower(parsedURL.Hostname()), ".")
	if host == "" {
		return nil, fmt.Errorf("%w: host is required", ErrInvalidURL)
	}
	if isLocalHostname(host) {
		return nil, fmt.Errorf("%w: local hostname", ErrUnsupportedURL)
	}
	if address, err := netip.ParseAddr(host); err == nil && !isPublicAddress(address) {
		return nil, fmt.Errorf("%w: non-public IP address", ErrUnsupportedURL)
	}

	query, err := url.ParseQuery(parsedURL.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed query", ErrInvalidURL)
	}
	for parameter := range query {
		normalizedParameter := strings.ToLower(parameter)
		if _, sensitive := sensitiveParameters[normalizedParameter]; sensitive {
			return nil, fmt.Errorf("%w: sensitive query parameter", ErrUnsupportedURL)
		}
		if _, tracking := trackingParameters[normalizedParameter]; tracking {
			delete(query, parameter)
		}
	}

	parsedURL.Scheme = strings.ToLower(parsedURL.Scheme)
	parsedURL.Fragment = ""
	parsedURL.ForceQuery = false
	parsedURL.RawQuery = query.Encode()
	parsedURL.Host = normalizedHost(parsedURL, host)
	return parsedURL, nil
}

// Validate normalizes a URL and confirms that its host only resolves to public addresses.
func (p Policy) Validate(ctx context.Context, rawURL string) (*url.URL, error) {
	normalizedURL, err := p.Normalize(rawURL)
	if err != nil {
		return nil, err
	}

	host := normalizedURL.Hostname()
	if address, err := netip.ParseAddr(host); err == nil {
		if !isPublicAddress(address) {
			return nil, fmt.Errorf("%w: non-public IP address", ErrUnsupportedURL)
		}
		return normalizedURL, nil
	}

	addresses, err := p.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("%w: hostname has no public DNS answer", ErrUnsupportedURL)
	}
	for _, address := range addresses {
		if !isPublicAddress(address) {
			return nil, fmt.Errorf("%w: hostname resolves to a non-public IP address", ErrUnsupportedURL)
		}
	}
	return normalizedURL, nil
}

func isLocalHostname(host string) bool {
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local")
}

func isPublicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return address.IsGlobalUnicast() && !address.IsPrivate() && !address.IsLinkLocalUnicast() && !address.IsMulticast() && !address.IsUnspecified()
}

func normalizedHost(parsedURL *url.URL, hostname string) string {
	port := parsedURL.Port()
	if port == "" {
		if address, err := netip.ParseAddr(hostname); err == nil && address.Is6() {
			return "[" + hostname + "]"
		}
		return hostname
	}
	return net.JoinHostPort(hostname, port)
}

func hasValidPort(parsedURL *url.URL) bool {
	host := parsedURL.Host
	if strings.HasPrefix(host, "[") {
		closingBracket := strings.LastIndex(host, "]")
		if closingBracket == -1 {
			return false
		}
		suffix := host[closingBracket+1:]
		return suffix == "" || (strings.HasPrefix(suffix, ":") && isNumeric(suffix[1:]))
	}
	if !strings.Contains(host, ":") {
		return true
	}
	_, port, err := net.SplitHostPort(host)
	return err == nil && isNumeric(port)
}

func isNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
