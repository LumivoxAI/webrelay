package provider

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/LumivoxAI/webrelay/internal/config"
	"golang.org/x/net/proxy"
)

// NewHTTPClient creates one isolated outbound client for a configured provider.
func NewHTTPClient(timeout time.Duration, rawProxy string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if rawProxy != "" {
		proxyURL, err := url.Parse(rawProxy)
		if err != nil {
			return nil, fmt.Errorf("parse provider proxy: %w", err)
		}
		switch proxyURL.Scheme {
		case "https":
			transport.Proxy = http.ProxyURL(proxyURL)
		case "socks5":
			var authentication *proxy.Auth
			if proxyURL.User != nil {
				password, _ := proxyURL.User.Password()
				authentication = &proxy.Auth{User: proxyURL.User.Username(), Password: password}
			}
			dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, authentication, proxy.Direct)
			if err != nil {
				return nil, fmt.Errorf("create SOCKS5 proxy dialer: %w", err)
			}
			transport.Proxy = nil
			transport.DialContext = dialContext(dialer)
		default:
			return nil, fmt.Errorf("unsupported provider proxy scheme")
		}
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

// NewConfiguredClients creates isolated clients that share the configured outbound proxy.
func NewConfiguredClients(cfg config.Config) (map[Key]*http.Client, error) {
	clients := make(map[Key]*http.Client, 9)
	for _, settings := range []struct {
		key     Key
		timeout time.Duration
	}{
		{key: Key{Provider: EXA, Action: SEARCH}, timeout: cfg.Providers.Exa.Search.Timeout.Std()},
		{key: Key{Provider: EXA, Action: CONTENTS}, timeout: cfg.Providers.Exa.Contents.Timeout.Std()},
		{key: Key{Provider: BRAVE, Action: SEARCH}, timeout: cfg.Providers.Brave.Search.Timeout.Std()},
		{key: Key{Provider: MARKDOWN_NEW, Action: FETCH}, timeout: cfg.Providers.MarkdownNew.Fetch.Timeout.Std()},
		{key: Key{Provider: TINYFISH, Action: SEARCH}, timeout: cfg.Providers.TinyFish.Search.Timeout.Std()},
		{key: Key{Provider: TINYFISH, Action: FETCH}, timeout: cfg.Providers.TinyFish.Fetch.Timeout.Std()},
		{key: Key{Provider: TAVILY, Action: SEARCH}, timeout: cfg.Providers.Tavily.Search.Timeout.Std()},
		{key: Key{Provider: TAVILY, Action: EXTRACT}, timeout: cfg.Providers.Tavily.Extract.Timeout.Std()},
		{key: Key{Provider: FIRECRAWL, Action: SEARCH}, timeout: cfg.Providers.Firecrawl.Search.Timeout.Std()},
		{key: Key{Provider: FIRECRAWL, Action: SCRAPE}, timeout: cfg.Providers.Firecrawl.Scrape.Timeout.Std()},
	} {
		client, err := NewHTTPClient(settings.timeout, cfg.Proxy.URL)
		if err != nil {
			return nil, err
		}
		clients[settings.key] = client
	}
	return clients, nil
}

func dialContext(dialer proxy.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(_ context.Context, network, address string) (net.Conn, error) {
		return dialer.Dial(network, address)
	}
}
