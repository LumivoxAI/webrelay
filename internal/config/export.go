package config

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExportDefaultYAML returns a fully expanded starter configuration with field comments.
func ExportDefaultYAML() ([]byte, error) {
	cfg := Default()
	cfg.Providers.Exa.APIKey = "${EXA_API_KEY}"
	cfg.Providers.Brave.APIKey = "${BRAVE_API_KEY}"
	cfg.Providers.TinyFish.APIKey = "${TINYFISH_API_KEY}"
	cfg.Providers.Tavily.APIKey = "${TAVILY_API_KEY}"
	cfg.Providers.Firecrawl.APIKey = "${FIRECRAWL_API_KEY}"

	document, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encode default configuration: %w", err)
	}
	return addConfigComments(document), nil
}

func addConfigComments(document []byte) []byte {
	var output bytes.Buffer
	for _, line := range strings.Split(strings.TrimSuffix(string(document), "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-") {
			if key, _, found := strings.Cut(trimmed, ":"); found {
				if comment := configFieldComments[key]; comment != "" {
					indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
					_, _ = fmt.Fprintf(&output, "%s# %s\n", indent, comment)
				}
			}
		}
		output.WriteString(line)
		output.WriteByte('\n')
	}
	return output.Bytes()
}

var configFieldComments = map[string]string{
	"server":                 "Incoming HTTP server settings.",
	"listen":                 "Bind address in host:port form. Defaults to loopback.",
	"request_timeout":        "Maximum duration of one public HTTP request.",
	"shutdown_timeout":       "Maximum graceful shutdown duration.",
	"max_request_body_bytes": "Maximum JSON request body size; supports byte-size suffixes such as mb.",
	"proxy":                  "Optional common proxy for every outbound provider request. Supports https and socks5.",
	"url":                    "Leave empty for direct connections. Credentials in this URL are treated as secrets.",
	"search":                 "Public search settings or a provider search action configuration.",
	"default_limit":          "Result count used when the client omits limit.",
	"max_limit":              "Maximum public search result count; cannot exceed 20.",
	"providers":              "Ordered route providers or all provider definitions at the root.",
	"content":                "Document pagination limits and sequential content fallback order.",
	"default_chunk_chars":    "Character count returned when the client omits content limit.",
	"max_chunk_chars":        "Maximum character count returned by one content request.",
	"max_document_chars":     "Maximum text retained for one cached document.",
	"exa":                    "Exa provider configuration. Set EXA_API_KEY or replace the placeholder.",
	"brave":                  "Brave Search configuration. Set BRAVE_API_KEY or replace the placeholder.",
	"markdown_new":           "markdown.new content extraction configuration; no API key is required.",
	"tinyfish":               "TinyFish provider configuration. Set TINYFISH_API_KEY or replace the placeholder.",
	"tavily":                 "Tavily provider configuration. Set TAVILY_API_KEY or replace the placeholder.",
	"firecrawl":              "Firecrawl provider configuration. Set FIRECRAWL_API_KEY or replace the placeholder.",
	"enabled":                "Disable this provider or individual action without removing its settings.",
	"api_key":                "API key or ${ENVIRONMENT_VARIABLE}; do not commit real secrets.",
	"base_url":               "HTTPS endpoint for markdown.new.",
	"search_type":            "Exa search mode: instant, fast, auto, deep-lite, deep, or deep-reasoning.",
	"search_with_text":       "Store embedded text returned by Exa Search in the document cache.",
	"search_with_highlights": "Request Exa highlights for search result snippets.",
	"max_content_characters": "Maximum text requested from this Exa action; cannot exceed content.max_document_chars.",
	"max_age_hours":          "Exa Contents cache policy: null uses Exa default, 0 forces live crawl, -1 avoids it.",
	"timeout":                "Per-attempt upstream timeout.",
	"max_attempts":           "Total attempts for this action before fallback, including the first request.",
	"initial_backoff":        "Initial delay before a retryable retry.",
	"max_backoff":            "Maximum exponential retry delay.",
	"failure_threshold":      "Consecutive retryable failures before this action enters cooldown.",
	"cooldown":               "Cooldown after reaching the failure threshold.",
	"quota_cooldown":         "Cooldown used after an upstream quota exhaustion response.",
	"country":                "Brave country code used to localize results.",
	"search_lang":            "Brave result language preference.",
	"ui_lang":                "Brave interface language preference.",
	"safesearch":             "Brave safe search mode: off, moderate, or strict.",
	"spellcheck":             "Allow Brave to correct spelling in search requests.",
	"rate_limit_cooldown":    "Long markdown.new cooldown after its daily rate limit is exhausted.",
	"method":                 "markdown.new extraction method: auto, ai, or browser.",
	"retain_images":          "Keep image references in markdown.new output.",
	"min_content_chars":      "Short markdown.new error or block responses below this threshold are rejected.",
	"location":               "Optional TinyFish search location.",
	"language":               "TinyFish search language.",
	"domain_type":            "TinyFish search domain type: web, news, or research_paper.",
	"format":                 "Selected log or content format for this section.",
	"use_gateway_ttl":        "Pass the gateway document TTL to TinyFish Fetch.",
	"search_depth":           "Tavily search depth: basic, advanced, fast, or ultra-fast.",
	"auto_parameters":        "Allow Tavily to infer search parameters automatically.",
	"topic":                  "Tavily search topic: general, news, or finance.",
	"include_raw_content":    "Request Tavily raw content with search results; normally disabled.",
	"include_usage":          "Request provider usage credits for internal diagnostics.",
	"extract_depth":          "Tavily extraction depth: basic or advanced.",
	"scrape":                 "Firecrawl document scraping action settings.",
	"fetch":                  "Content extraction action settings.",
	"extract":                "Content extraction action settings.",
	"contents":               "Exa document contents action settings.",
	"cache":                  "Persistent SQLite cache settings.",
	"type":                   "Cache implementation; only sqlite is supported.",
	"path":                   "SQLite database path, resolved from the current XDG environment.",
	"search_ttl":             "Lifetime of cached search responses.",
	"document_ttl":           "Lifetime of cached document content.",
	"cleanup_interval":       "Interval for expired-cache cleanup and size eviction.",
	"max_size_mb":            "Maximum cache size or single log file size in megabytes.",
	"logging":                "Structured logging output settings.",
	"level":                  "Minimum log level: debug, info, warn, or error.",
	"file":                   "Rotating log file path, resolved from the current XDG environment. Empty disables file output.",
	"console":                "Also write logs to stderr.",
	"rotation":               "Retention policy for the rotating log file.",
	"max_backups":            "Number of rotated log files to retain.",
	"max_age_days":           "Maximum age of rotated log files in days.",
	"compress":               "Compress rotated log files.",
}
