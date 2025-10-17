package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	ACTION_SEARCH   = "search"
	ACTION_FETCH    = "fetch"
	ACTION_EXTRACT  = "extract"
	ACTION_CONTENTS = "contents"
	ACTION_SCRAPE   = "scrape"
)

// ProvidersConfig contains connection settings shared by provider actions.
type ProvidersConfig struct {
	Exa         ExaConfig         `yaml:"exa"`
	Brave       BraveConfig       `yaml:"brave"`
	MarkdownNew MarkdownNewConfig `yaml:"markdown_new"`
	TinyFish    TinyFishConfig    `yaml:"tinyfish"`
	Tavily      TavilyConfig      `yaml:"tavily"`
	Firecrawl   FirecrawlConfig   `yaml:"firecrawl"`
}

func DefaultProvidersConfig() ProvidersConfig {
	return ProvidersConfig{
		Exa:         DefaultExaConfig(),
		Brave:       DefaultBraveConfig(),
		MarkdownNew: DefaultMarkdownNewConfig(),
		TinyFish:    DefaultTinyFishConfig(),
		Tavily:      DefaultTavilyConfig(),
		Firecrawl:   DefaultFirecrawlConfig(),
	}
}

// ActionConfig contains routing settings independently applied to one action.
type ActionConfig struct {
	Enabled          bool     `yaml:"enabled"`
	Timeout          Duration `yaml:"timeout"`
	MaxAttempts      int      `yaml:"max_attempts"`
	InitialBackoff   Duration `yaml:"initial_backoff"`
	MaxBackoff       Duration `yaml:"max_backoff"`
	FailureThreshold int      `yaml:"failure_threshold"`
	Cooldown         Duration `yaml:"cooldown"`
	QuotaCooldown    Duration `yaml:"quota_cooldown"`
}

func DefaultActionConfig(timeout time.Duration, attempts, threshold int, cooldown time.Duration) ActionConfig {
	return ActionConfig{
		Enabled:          true,
		Timeout:          Duration(timeout),
		MaxAttempts:      attempts,
		InitialBackoff:   Duration(250 * time.Millisecond),
		MaxBackoff:       Duration(2 * time.Second),
		FailureThreshold: threshold,
		Cooldown:         Duration(cooldown),
		QuotaCooldown:    Duration(time.Hour),
	}
}

func (c ActionConfig) Validate() error {
	if err := validatePositive("timeout", c.Timeout.Std()); err != nil {
		return err
	}
	if c.MaxAttempts < 1 {
		return fmt.Errorf("max_attempts must be positive")
	}
	if err := validatePositive("initial_backoff", c.InitialBackoff.Std()); err != nil {
		return err
	}
	if err := validatePositive("max_backoff", c.MaxBackoff.Std()); err != nil {
		return err
	}
	if c.MaxBackoff.Std() < c.InitialBackoff.Std() {
		return fmt.Errorf("max_backoff must be no less than initial_backoff")
	}
	if c.FailureThreshold < 1 {
		return fmt.Errorf("failure_threshold must be positive")
	}
	if err := validatePositive("cooldown", c.Cooldown.Std()); err != nil {
		return err
	}
	return validatePositive("quota_cooldown", c.QuotaCooldown.Std())
}

type ExaConfig struct {
	Enabled  bool              `yaml:"enabled"`
	APIKey   string            `yaml:"api_key"`
	Search   ExaSearchConfig   `yaml:"search"`
	Contents ExaContentsConfig `yaml:"contents"`
}

type ExaSearchConfig struct {
	ActionConfig         `yaml:",inline"`
	SearchType           string `yaml:"search_type"`
	SearchWithText       bool   `yaml:"search_with_text"`
	SearchWithHighlights bool   `yaml:"search_with_highlights"`
	MaxContentCharacters int    `yaml:"max_content_characters"`
}

type ExaContentsConfig struct {
	ActionConfig         `yaml:",inline"`
	MaxContentCharacters int  `yaml:"max_content_characters"`
	MaxAgeHours          *int `yaml:"max_age_hours"`
}

func DefaultExaConfig() ExaConfig {
	return ExaConfig{
		Enabled: true,
		Search: ExaSearchConfig{
			ActionConfig:         DefaultActionConfig(20*time.Second, 2, 3, 5*time.Minute),
			SearchType:           "auto",
			SearchWithText:       true,
			SearchWithHighlights: true,
			MaxContentCharacters: 500000,
		},
		Contents: ExaContentsConfig{
			ActionConfig:         DefaultActionConfig(20*time.Second, 1, 2, 5*time.Minute),
			MaxContentCharacters: 500000,
		},
	}
}

func (p ExaSearchConfig) Validate(maxDocumentChars int) error {
	if !oneOf(p.SearchType, "instant", "fast", "auto", "deep-lite", "deep", "deep-reasoning") {
		return fmt.Errorf("search_type is invalid")
	}
	if p.MaxContentCharacters < 1 || p.MaxContentCharacters > maxDocumentChars {
		return fmt.Errorf("max_content_characters must be positive and no greater than content.max_document_chars")
	}
	return p.ActionConfig.Validate()
}

func (p ExaContentsConfig) Validate(maxDocumentChars int) error {
	if p.MaxContentCharacters < 1 || p.MaxContentCharacters > maxDocumentChars {
		return fmt.Errorf("max_content_characters must be positive and no greater than content.max_document_chars")
	}
	if p.MaxAgeHours != nil && *p.MaxAgeHours < -1 {
		return fmt.Errorf("max_age_hours must be at least -1")
	}
	return p.ActionConfig.Validate()
}

type BraveConfig struct {
	Enabled bool              `yaml:"enabled"`
	APIKey  string            `yaml:"api_key"`
	Search  BraveSearchConfig `yaml:"search"`
}

type BraveSearchConfig struct {
	ActionConfig `yaml:",inline"`
	Country      string `yaml:"country"`
	SearchLang   string `yaml:"search_lang"`
	UILang       string `yaml:"ui_lang"`
	Safesearch   string `yaml:"safesearch"`
	Spellcheck   bool   `yaml:"spellcheck"`
}

func DefaultBraveConfig() BraveConfig {
	return BraveConfig{
		Enabled: true,
		Search: BraveSearchConfig{
			ActionConfig: DefaultActionConfig(10*time.Second, 2, 3, 5*time.Minute),
			Country:      "RU",
			SearchLang:   "en",
			UILang:       "en-US",
			Safesearch:   "moderate",
			Spellcheck:   true,
		},
	}
}

func (p BraveSearchConfig) Validate() error {
	if !oneOf(p.Safesearch, "off", "moderate", "strict") {
		return fmt.Errorf("safesearch is invalid")
	}
	return p.ActionConfig.Validate()
}

type MarkdownNewConfig struct {
	Enabled bool                   `yaml:"enabled"`
	BaseURL string                 `yaml:"base_url"`
	Fetch   MarkdownNewFetchConfig `yaml:"fetch"`
}

type MarkdownNewFetchConfig struct {
	ActionConfig      `yaml:",inline"`
	RateLimitCooldown Duration `yaml:"rate_limit_cooldown"`
	Method            string   `yaml:"method"`
	RetainImages      bool     `yaml:"retain_images"`
	MinContentChars   int      `yaml:"min_content_chars"`
}

func DefaultMarkdownNewConfig() MarkdownNewConfig {
	return MarkdownNewConfig{
		Enabled: true,
		BaseURL: "https://markdown.new/",
		Fetch: MarkdownNewFetchConfig{
			ActionConfig:      DefaultActionConfig(20*time.Second, 1, 2, 2*time.Minute),
			RateLimitCooldown: Duration(time.Hour),
			Method:            "auto",
			MinContentChars:   100,
		},
	}
}

func (p MarkdownNewFetchConfig) Validate() error {
	if !oneOf(p.Method, "auto", "ai", "browser") {
		return fmt.Errorf("method is invalid")
	}
	if p.MinContentChars < 0 {
		return fmt.Errorf("min_content_chars must not be negative")
	}
	if err := p.ActionConfig.Validate(); err != nil {
		return err
	}
	return validatePositive("rate_limit_cooldown", p.RateLimitCooldown.Std())
}

// TinyFishConfig reserves independent settings for its future Search and Fetch clients.
type TinyFishConfig struct {
	Enabled bool         `yaml:"enabled"`
	APIKey  string       `yaml:"api_key"`
	Search  ActionConfig `yaml:"search"`
	Fetch   ActionConfig `yaml:"fetch"`
}

// TavilyConfig reserves independent settings for its future Search and Extract clients.
type TavilyConfig struct {
	Enabled bool         `yaml:"enabled"`
	APIKey  string       `yaml:"api_key"`
	Search  ActionConfig `yaml:"search"`
	Extract ActionConfig `yaml:"extract"`
}

// FirecrawlConfig reserves independent settings for its future Search and Scrape clients.
type FirecrawlConfig struct {
	Enabled bool         `yaml:"enabled"`
	APIKey  string       `yaml:"api_key"`
	Search  ActionConfig `yaml:"search"`
	Scrape  ActionConfig `yaml:"scrape"`
}

func DefaultTinyFishConfig() TinyFishConfig {
	return TinyFishConfig{
		Enabled: true,
		Search:  DefaultActionConfig(10*time.Second, 2, 3, 5*time.Minute),
		Fetch:   DefaultActionConfig(20*time.Second, 1, 2, 2*time.Minute),
	}
}
func DefaultTavilyConfig() TavilyConfig {
	return TavilyConfig{
		Enabled: true,
		Search:  DefaultActionConfig(15*time.Second, 2, 3, 5*time.Minute),
		Extract: DefaultActionConfig(20*time.Second, 1, 2, 5*time.Minute),
	}
}
func DefaultFirecrawlConfig() FirecrawlConfig {
	return FirecrawlConfig{
		Enabled: true,
		Search:  DefaultActionConfig(15*time.Second, 2, 3, 5*time.Minute),
		Scrape:  DefaultActionConfig(30*time.Second, 1, 2, 5*time.Minute),
	}
}

func (c ProvidersConfig) Validate(maxDocumentChars int) map[string]string {
	issues := make(map[string]string)
	c.validateExa(issues, maxDocumentChars)
	c.validateBrave(issues)
	c.validateMarkdownNew(issues)
	c.validateKeyActions(issues, "tinyfish", c.TinyFish.Enabled, c.TinyFish.APIKey, map[string]ActionConfig{ACTION_SEARCH: c.TinyFish.Search, ACTION_FETCH: c.TinyFish.Fetch})
	c.validateKeyActions(issues, "tavily", c.Tavily.Enabled, c.Tavily.APIKey, map[string]ActionConfig{ACTION_SEARCH: c.Tavily.Search, ACTION_EXTRACT: c.Tavily.Extract})
	c.validateKeyActions(issues, "firecrawl", c.Firecrawl.Enabled, c.Firecrawl.APIKey, map[string]ActionConfig{ACTION_SEARCH: c.Firecrawl.Search, ACTION_SCRAPE: c.Firecrawl.Scrape})
	return issues
}

func (c ProvidersConfig) validateExa(issues map[string]string, maxDocumentChars int) {
	c.validateKeyAction(issues, "exa", ACTION_SEARCH, c.Exa.Enabled, c.Exa.APIKey, c.Exa.Search.Enabled, c.Exa.Search.Validate(maxDocumentChars))
	c.validateKeyAction(issues, "exa", ACTION_CONTENTS, c.Exa.Enabled, c.Exa.APIKey, c.Exa.Contents.Enabled, c.Exa.Contents.Validate(maxDocumentChars))
}
func (c ProvidersConfig) validateBrave(issues map[string]string) {
	c.validateKeyAction(issues, "brave", ACTION_SEARCH, c.Brave.Enabled, c.Brave.APIKey, c.Brave.Search.Enabled, c.Brave.Search.Validate())
}
func (c ProvidersConfig) validateMarkdownNew(issues map[string]string) {
	parsedURL, err := url.Parse(c.MarkdownNew.BaseURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		err = fmt.Errorf("base_url must be an HTTPS URL")
	}
	c.validateKeyAction(issues, "markdown_new", ACTION_FETCH, c.MarkdownNew.Enabled, "configured", c.MarkdownNew.Fetch.Enabled, firstError(err, c.MarkdownNew.Fetch.Validate()))
}
func (c ProvidersConfig) validateKeyActions(issues map[string]string, name string, enabled bool, apiKey string, actions map[string]ActionConfig) {
	for action, settings := range actions {
		c.validateKeyAction(issues, name, action, enabled, apiKey, settings.Enabled, settings.Validate())
	}
}
func (c ProvidersConfig) validateKeyAction(issues map[string]string, name, action string, providerEnabled bool, apiKey string, actionEnabled bool, err error) {
	key := name + "/" + action
	if !providerEnabled || !actionEnabled {
		issues[key] = "disabled"
		return
	}
	if strings.TrimSpace(apiKey) == "" {
		issues[key] = "api_key is required"
		return
	}
	if err != nil {
		issues[key] = err.Error()
	}
}
func firstError(first, second error) error {
	if first != nil {
		return first
	}
	return second
}
