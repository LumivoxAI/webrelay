package provider

import (
	"github.com/LumivoxAI/webrelay/internal/config"
	"go.uber.org/zap"
)

// NewConfiguredManager creates independent state for every provider action.
func NewConfiguredManager(cfg config.Config, logger *zap.Logger) *Manager {
	settings := []struct {
		key               Key
		enabled           bool
		config            config.ActionConfig
		rateLimitCooldown config.Duration
	}{
		{key: Key{Provider: EXA, Action: SEARCH}, enabled: cfg.Providers.Exa.Enabled && cfg.Providers.Exa.Search.Enabled, config: cfg.Providers.Exa.Search.ActionConfig},
		{key: Key{Provider: EXA, Action: CONTENTS}, enabled: cfg.Providers.Exa.Enabled && cfg.Providers.Exa.Contents.Enabled, config: cfg.Providers.Exa.Contents.ActionConfig},
		{key: Key{Provider: BRAVE, Action: SEARCH}, enabled: cfg.Providers.Brave.Enabled && cfg.Providers.Brave.Search.Enabled, config: cfg.Providers.Brave.Search.ActionConfig},
		{key: Key{Provider: MARKDOWN_NEW, Action: FETCH}, enabled: cfg.Providers.MarkdownNew.Enabled && cfg.Providers.MarkdownNew.Fetch.Enabled, config: cfg.Providers.MarkdownNew.Fetch.ActionConfig, rateLimitCooldown: cfg.Providers.MarkdownNew.Fetch.RateLimitCooldown},
		{key: Key{Provider: TINYFISH, Action: SEARCH}, enabled: cfg.Providers.TinyFish.Enabled && cfg.Providers.TinyFish.Search.Enabled, config: cfg.Providers.TinyFish.Search.ActionConfig},
		{key: Key{Provider: TINYFISH, Action: FETCH}, enabled: cfg.Providers.TinyFish.Enabled && cfg.Providers.TinyFish.Fetch.Enabled, config: cfg.Providers.TinyFish.Fetch.ActionConfig},
		{key: Key{Provider: TAVILY, Action: SEARCH}, enabled: cfg.Providers.Tavily.Enabled && cfg.Providers.Tavily.Search.Enabled, config: cfg.Providers.Tavily.Search.ActionConfig},
		{key: Key{Provider: TAVILY, Action: EXTRACT}, enabled: cfg.Providers.Tavily.Enabled && cfg.Providers.Tavily.Extract.Enabled, config: cfg.Providers.Tavily.Extract.ActionConfig},
		{key: Key{Provider: FIRECRAWL, Action: SEARCH}, enabled: cfg.Providers.Firecrawl.Enabled && cfg.Providers.Firecrawl.Search.Enabled, config: cfg.Providers.Firecrawl.Search},
		{key: Key{Provider: FIRECRAWL, Action: SCRAPE}, enabled: cfg.Providers.Firecrawl.Enabled && cfg.Providers.Firecrawl.Scrape.Enabled, config: cfg.Providers.Firecrawl.Scrape},
	}
	initial := make(map[Key]State, len(settings))
	policies := make(map[Key]Policy, len(settings))
	for _, setting := range settings {
		initial[setting.key] = configuredState(cfg, setting.key, setting.enabled)
		policies[setting.key] = policy(setting.config, setting.rateLimitCooldown)
	}
	return NewManagerWithMetrics(initial, policies, NewMetrics(logger))
}

func configuredState(cfg config.Config, key Key, enabled bool) State {
	if !enabled {
		return STATE_DISABLED
	}
	if !cfg.ProviderActionAvailable(string(key.Provider), string(key.Action)) {
		return STATE_MISCONFIGURED
	}
	return STATE_AVAILABLE
}

func policy(settings config.ActionConfig, rateLimitCooldown config.Duration) Policy {
	limitCooldown := settings.Cooldown.Std()
	if rateLimitCooldown.Std() > 0 {
		limitCooldown = rateLimitCooldown.Std()
	}
	return Policy{
		MaxAttempts:       settings.MaxAttempts,
		InitialBackoff:    settings.InitialBackoff.Std(),
		MaxBackoff:        settings.MaxBackoff.Std(),
		FailureThreshold:  settings.FailureThreshold,
		Cooldown:          settings.Cooldown.Std(),
		QuotaCooldown:     settings.QuotaCooldown.Std(),
		RateLimitCooldown: limitCooldown,
	}
}
