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

// NewConfiguredClients creates isolated clients for all providers in a runtime config.
func NewConfiguredClients(cfg config.Config) (map[Name]*http.Client, error) {
	clients := make(map[Name]*http.Client, 3)
	for _, settings := range []struct {
		name    Name
		timeout time.Duration
		proxy   string
	}{
		{name: EXA, timeout: cfg.Providers.Exa.Timeout.Std(), proxy: cfg.Providers.Exa.Proxy},
		{name: BRAVE, timeout: cfg.Providers.Brave.Timeout.Std(), proxy: cfg.Providers.Brave.Proxy},
		{name: MARKDOWN_NEW, timeout: cfg.Providers.MarkdownNew.Timeout.Std(), proxy: cfg.Providers.MarkdownNew.Proxy},
	} {
		client, err := NewHTTPClient(settings.timeout, settings.proxy)
		if err != nil {
			return nil, err
		}
		clients[settings.name] = client
	}
	return clients, nil
}

func dialContext(dialer proxy.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(_ context.Context, network, address string) (net.Conn, error) {
		return dialer.Dial(network, address)
	}
}
