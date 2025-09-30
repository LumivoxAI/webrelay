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

func validateProviderTiming(timeout Duration, attempts, threshold int, cooldown Duration) error {
	if err := validatePositive("timeout", timeout.Std()); err != nil {
		return err
	}
	if attempts < 1 {
		return fmt.Errorf("max_attempts must be positive")
	}
	if threshold < 1 {
		return fmt.Errorf("failure_threshold must be positive")
	}
	return validatePositive("cooldown", cooldown.Std())
}

func validProxy(raw string) bool {
	if raw == "" {
		return true
	}
	proxyURL, err := url.Parse(raw)
	return err == nil && (proxyURL.Scheme == "https" || proxyURL.Scheme == "socks5") && proxyURL.Host != ""
}

// ExaConfig controls both Exa Search and Exa Contents requests.
type ExaConfig struct {
	Enabled              bool     `yaml:"enabled"`
	APIKey               string   `yaml:"api_key"`
	Proxy                string   `yaml:"proxy"`
	SearchType           string   `yaml:"search_type"`
	Timeout              Duration `yaml:"timeout"`
	MaxAttempts          int      `yaml:"max_attempts"`
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
		FailureThreshold:     3,
		Cooldown:             Duration(5 * time.Minute),
		QuotaCooldown:        Duration(time.Hour),
		SearchWithText:       true,
		SearchWithHighlights: true,
		MaxContentCharacters: 500000,
	}
}

// Validate checks Exa settings without returning the API key or proxy URL.
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
	if err := validateProviderTiming(p.Timeout, p.MaxAttempts, p.FailureThreshold, p.Cooldown); err != nil {
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
	if !validProxy(p.Proxy) {
		return fmt.Errorf("proxy is invalid")
	}
	return nil
}

// BraveConfig controls Brave Search requests.
type BraveConfig struct {
	Enabled          bool     `yaml:"enabled"`
	APIKey           string   `yaml:"api_key"`
	Proxy            string   `yaml:"proxy"`
	Timeout          Duration `yaml:"timeout"`
	MaxAttempts      int      `yaml:"max_attempts"`
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
		FailureThreshold: 3,
		Cooldown:         Duration(5 * time.Minute),
		Country:          "RU",
		SearchLang:       "en",
		UILang:           "en-US",
		Safesearch:       "moderate",
		Spellcheck:       true,
	}
}

// Validate checks Brave settings without returning the API key or proxy URL.
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
	if err := validateProviderTiming(p.Timeout, p.MaxAttempts, p.FailureThreshold, p.Cooldown); err != nil {
		return err
	}
	if !validProxy(p.Proxy) {
		return fmt.Errorf("proxy is invalid")
	}
	return nil
}

// MarkdownNewConfig controls markdown.new content extraction requests.
type MarkdownNewConfig struct {
	Enabled           bool     `yaml:"enabled"`
	BaseURL           string   `yaml:"base_url"`
	Method            string   `yaml:"method"`
	RetainImages      bool     `yaml:"retain_images"`
	Proxy             string   `yaml:"proxy"`
	Timeout           Duration `yaml:"timeout"`
	MaxAttempts       int      `yaml:"max_attempts"`
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
		Timeout:           Duration(20 * time.Second),
		MaxAttempts:       1,
		FailureThreshold:  2,
		Cooldown:          Duration(2 * time.Minute),
		RateLimitCooldown: Duration(time.Hour),
	}
}

// Validate checks markdown.new settings without returning the proxy URL.
func (p MarkdownNewConfig) Validate() error {
	if !p.Enabled {
		return fmt.Errorf("disabled")
	}
	if !oneOf(p.Method, "auto", "ai", "browser") {
		return fmt.Errorf("method is invalid")
	}
	parsedURL, err := url.Parse(p.BaseURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return fmt.Errorf("base_url must be an HTTPS URL")
	}
	if err := validateProviderTiming(p.Timeout, p.MaxAttempts, p.FailureThreshold, p.Cooldown); err != nil {
		return err
	}
	if err := validatePositive("rate_limit_cooldown", p.RateLimitCooldown.Std()); err != nil {
		return err
	}
	if !validProxy(p.Proxy) {
		return fmt.Errorf("proxy is invalid")
	}
	return nil
}
