package provider

import "github.com/LumivoxAI/webrelay/internal/config"

// NewConfiguredManager creates provider state from a validated runtime config.
func NewConfiguredManager(cfg config.Config) *Manager {
	initial := map[Name]State{
		EXA:          configuredState(cfg, EXA, cfg.Providers.Exa.Enabled),
		BRAVE:        configuredState(cfg, BRAVE, cfg.Providers.Brave.Enabled),
		MARKDOWN_NEW: configuredState(cfg, MARKDOWN_NEW, cfg.Providers.MarkdownNew.Enabled),
	}
	policies := map[Name]Policy{
		EXA: {
			MaxAttempts:       cfg.Providers.Exa.MaxAttempts,
			InitialBackoff:    cfg.Providers.Exa.InitialBackoff.Std(),
			MaxBackoff:        cfg.Providers.Exa.MaxBackoff.Std(),
			FailureThreshold:  cfg.Providers.Exa.FailureThreshold,
			Cooldown:          cfg.Providers.Exa.Cooldown.Std(),
			QuotaCooldown:     cfg.Providers.Exa.QuotaCooldown.Std(),
			RateLimitCooldown: cfg.Providers.Exa.Cooldown.Std(),
		},
		BRAVE: {
			MaxAttempts:       cfg.Providers.Brave.MaxAttempts,
			InitialBackoff:    cfg.Providers.Brave.InitialBackoff.Std(),
			MaxBackoff:        cfg.Providers.Brave.MaxBackoff.Std(),
			FailureThreshold:  cfg.Providers.Brave.FailureThreshold,
			Cooldown:          cfg.Providers.Brave.Cooldown.Std(),
			QuotaCooldown:     cfg.Providers.Brave.Cooldown.Std(),
			RateLimitCooldown: cfg.Providers.Brave.Cooldown.Std(),
		},
		MARKDOWN_NEW: {
			MaxAttempts:       cfg.Providers.MarkdownNew.MaxAttempts,
			InitialBackoff:    cfg.Providers.MarkdownNew.InitialBackoff.Std(),
			MaxBackoff:        cfg.Providers.MarkdownNew.MaxBackoff.Std(),
			FailureThreshold:  cfg.Providers.MarkdownNew.FailureThreshold,
			Cooldown:          cfg.Providers.MarkdownNew.Cooldown.Std(),
			QuotaCooldown:     cfg.Providers.MarkdownNew.RateLimitCooldown.Std(),
			RateLimitCooldown: cfg.Providers.MarkdownNew.RateLimitCooldown.Std(),
		},
	}
	return NewManager(initial, policies)
}

func configuredState(cfg config.Config, name Name, enabled bool) State {
	if !enabled {
		return STATE_DISABLED
	}
	if !cfg.ProviderAvailable(string(name)) {
		return STATE_MISCONFIGURED
	}
	return STATE_AVAILABLE
}
