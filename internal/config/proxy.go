package config

import (
	"fmt"
	"net/url"
)

// ProxyConfig controls the shared route for outbound provider requests.
type ProxyConfig struct {
	URL string `yaml:"url"`
}

// DefaultProxyConfig returns direct outbound connections.
func DefaultProxyConfig() ProxyConfig {
	return ProxyConfig{}
}

// Validate checks the proxy URL without exposing its credentials.
func (c ProxyConfig) Validate() error {
	if c.URL == "" {
		return nil
	}
	proxyURL, err := url.Parse(c.URL)
	if err != nil || (proxyURL.Scheme != "https" && proxyURL.Scheme != "socks5") || proxyURL.Host == "" {
		return fmt.Errorf("proxy.url is invalid")
	}
	return nil
}
