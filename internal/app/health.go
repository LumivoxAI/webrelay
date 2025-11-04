package app

import (
	"context"

	"github.com/LumivoxAI/webrelay/internal/cache"
	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/httpapi"
	"github.com/LumivoxAI/webrelay/internal/provider"
)

// ReadinessChecker reports whether the configured routing paths can accept work.
type ReadinessChecker struct {
	config  config.Config
	store   *cache.Store
	manager *provider.Manager
}

// NewReadinessChecker constructs a readiness probe without creating upstream clients.
func NewReadinessChecker(config config.Config, store *cache.Store, manager *provider.Manager) *ReadinessChecker {
	return &ReadinessChecker{config: config, store: store, manager: manager}
}

// Ready checks local dependencies and the current routing state without upstream requests.
func (c *ReadinessChecker) Ready(ctx context.Context) (httpapi.Readiness, bool) {
	if c == nil || c.manager == nil {
		return httpapi.Readiness{Status: "not_ready", SearchProviders: map[string]string{}, ContentProviders: map[string]string{}}, false
	}
	response := httpapi.Readiness{
		Status:           "ready",
		SearchProviders:  c.states(c.config.Search.Providers, provider.SEARCH),
		ContentProviders: c.contentStates(),
	}
	if c.store == nil || c.store.Ping(ctx) != nil || !hasAvailable(response.SearchProviders) || !hasAvailable(response.ContentProviders) {
		response.Status = "not_ready"
		return response, false
	}
	return response, true
}

func (c *ReadinessChecker) states(names []string, action provider.Action) map[string]string {
	states := make(map[string]string, len(names))
	for _, name := range names {
		states[name] = string(c.manager.State(provider.Key{Provider: provider.Name(name), Action: action}))
	}
	return states
}

func (c *ReadinessChecker) contentStates() map[string]string {
	states := make(map[string]string, len(c.config.Content.Providers))
	for _, name := range c.config.Content.Providers {
		states[name] = string(c.manager.State(provider.Key{Provider: provider.Name(name), Action: contentAction(name)}))
	}
	return states
}

func contentAction(name string) provider.Action {
	switch provider.Name(name) {
	case provider.TINYFISH, provider.MARKDOWN_NEW:
		return provider.FETCH
	case provider.TAVILY:
		return provider.EXTRACT
	case provider.EXA:
		return provider.CONTENTS
	case provider.FIRECRAWL:
		return provider.SCRAPE
	default:
		return ""
	}
}

func hasAvailable(states map[string]string) bool {
	for _, state := range states {
		if state == string(provider.STATE_AVAILABLE) {
			return true
		}
	}
	return false
}
