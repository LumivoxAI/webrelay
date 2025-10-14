package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ProvidersConfig contains provider-specific connection settings.
type ProvidersConfig struct {
	Exa         ExaConfig         `yaml:"exa"`
	Brave       BraveConfig       `yaml:"brave"`
	MarkdownNew MarkdownNewConfig `yaml:"markdown_new"`
}

// DefaultProvidersConfig returns defaults for all supported providers.
func DefaultProvidersConfig() ProvidersConfig {
	return ProvidersConfig{
		Exa:         DefaultExaConfig(),
		Brave:       DefaultBraveConfig(),
		MarkdownNew: DefaultMarkdownNewConfig(),
	}
}

// Validate returns safe provider issues without exposing credentials.
func (c ProvidersConfig) Validate(maxDocumentChars int) map[string]string {
	issues := make(map[string]string)
	if err := c.Exa.Validate(maxDocumentChars); err != nil {
		issues["exa"] = err.Error()
	}
	if err := c.Brave.Validate(); err != nil {
		issues["brave"] = err.Error()
	}
	if err := c.MarkdownNew.Validate(); err != nil {
		issues["markdown_new"] = err.Error()
	}
	return issues
}

func validateProviderTiming(timeout Duration, attempts int, initialBackoff, maxBackoff Duration, threshold int, cooldown Duration) error {
	if err := validatePositive("timeout", timeout.Std()); err != nil {
		return err
	}
	if attempts < 1 {
		return fmt.Errorf("max_attempts must be positive")
	}
	if err := validatePositive("initial_backoff", initialBackoff.Std()); err != nil {
		return err
	}
	if err := validatePositive("max_backoff", maxBackoff.Std()); err != nil {
		return err
	}
	if maxBackoff.Std() < initialBackoff.Std() {
		return fmt.Errorf("max_backoff must be no less than initial_backoff")
	}
	if threshold < 1 {
		return fmt.Errorf("failure_threshold must be positive")
	}
	return validatePositive("cooldown", cooldown.Std())
}

// ExaConfig controls both Exa Search and Exa Contents requests.
type ExaConfig struct {
	Enabled              bool     `yaml:"enabled"`
	APIKey               string   `yaml:"api_key"`
	SearchType           string   `yaml:"search_type"`
	Timeout              Duration `yaml:"timeout"`
	MaxAttempts          int      `yaml:"max_attempts"`
	InitialBackoff       Duration `yaml:"initial_backoff"`
	MaxBackoff           Duration `yaml:"max_backoff"`
	FailureThreshold     int      `yaml:"failure_threshold"`
	Cooldown             Duration `yaml:"cooldown"`
	QuotaCooldown        Duration `yaml:"quota_cooldown"`
	SearchWithText       bool     `yaml:"search_with_text"`
	SearchWithHighlights bool     `yaml:"search_with_highlights"`
	MaxContentCharacters int      `yaml:"max_content_characters"`
	MaxAgeHours          *int     `yaml:"max_age_hours"`
}

// DefaultExaConfig returns the default Exa routing and content limits.
func DefaultExaConfig() ExaConfig {
	return ExaConfig{
		Enabled:              true,
		SearchType:           "auto",
		Timeout:              Duration(20 * time.Second),
		MaxAttempts:          2,
		InitialBackoff:       Duration(250 * time.Millisecond),
		MaxBackoff:           Duration(2 * time.Second),
		FailureThreshold:     3,
		Cooldown:             Duration(5 * time.Minute),
		QuotaCooldown:        Duration(time.Hour),
		SearchWithText:       true,
		SearchWithHighlights: true,
		MaxContentCharacters: 500000,
	}
}

// Validate checks Exa settings without returning the API key.
func (p ExaConfig) Validate(maxDocumentChars int) error {
	if !p.Enabled {
		return fmt.Errorf("disabled")
	}
	if strings.TrimSpace(p.APIKey) == "" {
		return fmt.Errorf("api_key is required")
	}
	if !oneOf(p.SearchType, "instant", "fast", "auto", "deep-lite", "deep", "deep-reasoning") {
		return fmt.Errorf("search_type is invalid")
	}
	if err := validateProviderTiming(p.Timeout, p.MaxAttempts, p.InitialBackoff, p.MaxBackoff, p.FailureThreshold, p.Cooldown); err != nil {
		return err
	}
	if err := validatePositive("quota_cooldown", p.QuotaCooldown.Std()); err != nil {
		return err
	}
	if p.MaxContentCharacters < 1 || p.MaxContentCharacters > maxDocumentChars {
		return fmt.Errorf("max_content_characters must be positive and no greater than content.max_document_chars")
	}
	if p.MaxAgeHours != nil && *p.MaxAgeHours < -1 {
		return fmt.Errorf("max_age_hours must be at least -1")
	}
	return nil
}

// BraveConfig controls Brave Search requests.
type BraveConfig struct {
	Enabled          bool     `yaml:"enabled"`
	APIKey           string   `yaml:"api_key"`
	Timeout          Duration `yaml:"timeout"`
	MaxAttempts      int      `yaml:"max_attempts"`
	InitialBackoff   Duration `yaml:"initial_backoff"`
	MaxBackoff       Duration `yaml:"max_backoff"`
	FailureThreshold int      `yaml:"failure_threshold"`
	Cooldown         Duration `yaml:"cooldown"`
	Country          string   `yaml:"country"`
	SearchLang       string   `yaml:"search_lang"`
	UILang           string   `yaml:"ui_lang"`
	Safesearch       string   `yaml:"safesearch"`
	Spellcheck       bool     `yaml:"spellcheck"`
}

// DefaultBraveConfig returns the default Brave locale and retry settings.
func DefaultBraveConfig() BraveConfig {
	return BraveConfig{
		Enabled:          true,
		Timeout:          Duration(10 * time.Second),
		MaxAttempts:      2,
		InitialBackoff:   Duration(250 * time.Millisecond),
		MaxBackoff:       Duration(2 * time.Second),
		FailureThreshold: 3,
		Cooldown:         Duration(5 * time.Minute),
		Country:          "RU",
		SearchLang:       "en",
		UILang:           "en-US",
		Safesearch:       "moderate",
		Spellcheck:       true,
	}
}

// Validate checks Brave settings without returning the API key.
func (p BraveConfig) Validate() error {
	if !p.Enabled {
		return fmt.Errorf("disabled")
	}
	if strings.TrimSpace(p.APIKey) == "" {
		return fmt.Errorf("api_key is required")
	}
	if !oneOf(p.Safesearch, "off", "moderate", "strict") {
		return fmt.Errorf("safesearch is invalid")
	}
	if err := validateProviderTiming(p.Timeout, p.MaxAttempts, p.InitialBackoff, p.MaxBackoff, p.FailureThreshold, p.Cooldown); err != nil {
		return err
	}
	return nil
}

// MarkdownNewConfig controls markdown.new content extraction requests.
type MarkdownNewConfig struct {
	Enabled           bool     `yaml:"enabled"`
	BaseURL           string   `yaml:"base_url"`
	Method            string   `yaml:"method"`
	RetainImages      bool     `yaml:"retain_images"`
	MinContentChars   int      `yaml:"min_content_chars"`
	Timeout           Duration `yaml:"timeout"`
	MaxAttempts       int      `yaml:"max_attempts"`
	InitialBackoff    Duration `yaml:"initial_backoff"`
	MaxBackoff        Duration `yaml:"max_backoff"`
	FailureThreshold  int      `yaml:"failure_threshold"`
	Cooldown          Duration `yaml:"cooldown"`
	RateLimitCooldown Duration `yaml:"rate_limit_cooldown"`
}

// DefaultMarkdownNewConfig returns markdown.new extraction defaults.
func DefaultMarkdownNewConfig() MarkdownNewConfig {
	return MarkdownNewConfig{
		Enabled:           true,
		BaseURL:           "https://markdown.new/",
		Method:            "auto",
		MinContentChars:   100,
		Timeout:           Duration(20 * time.Second),
		MaxAttempts:       1,
		InitialBackoff:    Duration(250 * time.Millisecond),
		MaxBackoff:        Duration(2 * time.Second),
		FailureThreshold:  2,
		Cooldown:          Duration(2 * time.Minute),
		RateLimitCooldown: Duration(time.Hour),
	}
}

// Validate checks markdown.new settings.
func (p MarkdownNewConfig) Validate() error {
	if !p.Enabled {
		return fmt.Errorf("disabled")
	}
	if !oneOf(p.Method, "auto", "ai", "browser") {
		return fmt.Errorf("method is invalid")
	}
	if p.MinContentChars < 0 {
		return fmt.Errorf("min_content_chars must not be negative")
	}
	parsedURL, err := url.Parse(p.BaseURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return fmt.Errorf("base_url must be an HTTPS URL")
	}
	if err := validateProviderTiming(p.Timeout, p.MaxAttempts, p.InitialBackoff, p.MaxBackoff, p.FailureThreshold, p.Cooldown); err != nil {
		return err
	}
	if err := validatePositive("rate_limit_cooldown", p.RateLimitCooldown.Std()); err != nil {
		return err
	}
	return nil
}
