// Package search implements the cached public search workflow.
package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LumivoxAI/webrelay/internal/cache"
	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/LumivoxAI/webrelay/internal/urlpolicy"
	"go.uber.org/zap"
)

const (
	MAX_QUERY_CHARS = 400
	MAX_QUERY_WORDS = 50
	EXA_SEARCH      = "exa_search"
)

var (
	ErrInvalidQuery   = errors.New("invalid query")
	ErrInvalidRequest = errors.New("invalid search request")
)

// Request is the provider-neutral public search request.
type Request struct {
	Query           string
	Limit           *int
	IncludeDomains  []string
	ExcludeDomains  []string
	PublishedAfter  *time.Time
	PublishedBefore *time.Time
	ForceRefresh    bool
}

// Result is a normalized provider result. EmbeddedContent is stored locally
// only and is never included in a public search response.
type Result struct {
	Rank            int
	URL             string
	NormalizedURL   string
	Title           string
	Snippet         string
	PublishedAt     *time.Time
	EmbeddedContent string
}

// Response is a normalized provider search response.
type Response struct {
	Results []Result
}

// Client executes a single provider search operation.
type Client interface {
	Search(context.Context, Request) (Response, error)
}

// ClientFunc adapts a function to Client.
type ClientFunc func(context.Context, Request) (Response, error)

func (f ClientFunc) Search(ctx context.Context, request Request) (Response, error) {
	return f(ctx, request)
}

// Entry is the completed search returned to the HTTP layer.
type Entry struct {
	ID        string
	Query     string
	Provider  string
	Cached    bool
	CreatedAt time.Time
	ExpiresAt time.Time
	Results   []cache.SearchResult
}

// Service coordinates validation, cache access, and provider routing.
type Service struct {
	cache     *cache.Store
	manager   *provider.Manager
	clients   map[provider.Name]Client
	providers []provider.Name
	config    config.Config
	logger    *zap.Logger
	now       func() time.Time
}

type providerProfile struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Settings  any    `json:"settings"`
}

// New constructs a search workflow with configured providers.
func New(cfg config.Config, store *cache.Store, manager *provider.Manager, clients map[provider.Name]Client, logger *zap.Logger) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("search cache is required")
	}
	if manager == nil {
		return nil, fmt.Errorf("provider manager is required")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	providers := make([]provider.Name, len(cfg.Search.Providers))
	for index, name := range cfg.Search.Providers {
		providers[index] = provider.Name(name)
	}
	return &Service{cache: store, manager: manager, clients: clients, providers: providers, config: cfg, logger: logger, now: time.Now}, nil
}

// Search retrieves an existing cache entry or routes a request to providers.
func (s *Service) Search(ctx context.Context, request Request) (*Entry, error) {
	normalized, err := s.normalizeRequest(request)
	if err != nil {
		return nil, err
	}
	key, parameters, err := s.cacheKey(normalized)
	if err != nil {
		return nil, fmt.Errorf("create search cache key: %w", err)
	}
	if !normalized.ForceRefresh {
		entry, err := s.cache.GetSearch(ctx, key)
		if err != nil {
			return nil, err
		}
		if entry != nil {
			s.manager.Metrics().RecordCacheHit(provider.Key{Provider: provider.Name(entry.Provider), Action: provider.SEARCH})
			return entryFromCache(entry, true), nil
		}
	}

	var response Response
	routed, err := s.manager.Route(ctx, provider.SEARCH, s.providers, func(ctx context.Context, name provider.Name) error {
		client := s.clients[name]
		if client == nil {
			return &provider.Failure{Reason: provider.REASON_MISCONFIGURED}
		}
		upstream, err := client.Search(ctx, normalized)
		if err != nil {
			return err
		}
		response = normalizeResults(upstream.Results)
		return nil
	})
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	entry := cache.SearchEntry{
		Key:             key,
		OriginalQuery:   request.Query,
		NormalizedQuery: normalized.Query,
		Parameters:      parameters,
		Provider:        string(routed.Provider),
		CreatedAt:       now,
		ExpiresAt:       now.Add(s.config.Cache.SearchTTL.Std()),
		Results:         cacheResults(response.Results),
	}
	if routed.Provider == provider.EXA {
		if err := s.saveEmbeddedDocuments(ctx, response.Results, now); err != nil {
			return nil, err
		}
	}
	saved, err := s.cache.PutSearch(ctx, entry)
	if err != nil {
		return nil, err
	}
	return entryFromCache(saved, false), nil
}

func (s *Service) normalizeRequest(request Request) (Request, error) {
	request.Query = strings.Join(strings.Fields(request.Query), " ")
	if request.Query == "" || utf8.RuneCountInString(request.Query) > MAX_QUERY_CHARS || len(strings.Fields(request.Query)) > MAX_QUERY_WORDS {
		return Request{}, ErrInvalidQuery
	}
	limit := s.config.Search.DefaultLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	if limit < 1 || limit > s.config.Search.MaxLimit {
		return Request{}, ErrInvalidRequest
	}
	request.Limit = &limit
	var err error
	request.IncludeDomains, err = normalizeDomains(request.IncludeDomains)
	if err != nil {
		return Request{}, err
	}
	request.ExcludeDomains, err = normalizeDomains(request.ExcludeDomains)
	if err != nil {
		return Request{}, err
	}
	for _, include := range request.IncludeDomains {
		for _, exclude := range request.ExcludeDomains {
			if include == exclude {
				return Request{}, ErrInvalidRequest
			}
		}
	}
	if request.PublishedAfter != nil && request.PublishedBefore != nil && request.PublishedAfter.After(*request.PublishedBefore) {
		return Request{}, ErrInvalidRequest
	}
	return request, nil
}

func normalizeDomains(domains []string) ([]string, error) {
	seen := make(map[string]struct{}, len(domains))
	normalized := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if domain == "" || strings.ContainsAny(domain, "/:@?#") || net.ParseIP(domain) != nil || !validDomain(domain) {
			return nil, ErrInvalidRequest
		}
		if _, exists := seen[domain]; !exists {
			seen[domain] = struct{}{}
			normalized = append(normalized, domain)
		}
	}
	return normalized, nil
}

func validDomain(domain string) bool {
	if len(domain) > 253 || !strings.Contains(domain, ".") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
				return false
			}
		}
	}
	return true
}

func (s *Service) cacheKey(request Request) (string, []byte, error) {
	parameters, err := json.Marshal(struct {
		Query           string            `json:"query"`
		Limit           int               `json:"limit"`
		IncludeDomains  []string          `json:"include_domains"`
		ExcludeDomains  []string          `json:"exclude_domains"`
		PublishedAfter  *time.Time        `json:"published_after"`
		PublishedBefore *time.Time        `json:"published_before"`
		Profile         []providerProfile `json:"profile"`
	}{
		Query:           request.Query,
		Limit:           *request.Limit,
		IncludeDomains:  request.IncludeDomains,
		ExcludeDomains:  request.ExcludeDomains,
		PublishedAfter:  request.PublishedAfter,
		PublishedBefore: request.PublishedBefore,
		Profile:         s.providerProfile(),
	})
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(parameters)
	return hex.EncodeToString(digest[:]), parameters, nil
}

func (s *Service) providerProfile() []providerProfile {
	profile := make([]providerProfile, 0, len(s.config.Search.Providers))
	for _, name := range s.config.Search.Providers {
		settings := any(nil)
		switch name {
		case string(provider.EXA):
			settings = struct {
				SearchType           string `json:"search_type"`
				SearchWithText       bool   `json:"search_with_text"`
				SearchWithHighlights bool   `json:"search_with_highlights"`
			}{s.config.Providers.Exa.Search.SearchType, s.config.Providers.Exa.Search.SearchWithText, s.config.Providers.Exa.Search.SearchWithHighlights}
		case string(provider.TINYFISH):
			settings = struct {
				Location   string `json:"location"`
				Language   string `json:"language"`
				DomainType string `json:"domain_type"`
			}{s.config.Providers.TinyFish.Search.Location, s.config.Providers.TinyFish.Search.Language, s.config.Providers.TinyFish.Search.DomainType}
		case string(provider.TAVILY):
			settings = struct {
				SearchDepth    string `json:"search_depth"`
				AutoParameters bool   `json:"auto_parameters"`
				Topic          string `json:"topic"`
			}{s.config.Providers.Tavily.Search.SearchDepth, s.config.Providers.Tavily.Search.AutoParameters, s.config.Providers.Tavily.Search.Topic}
		case string(provider.FIRECRAWL):
			settings = struct{}{}
		case string(provider.BRAVE):
			settings = struct {
				Country    string `json:"country"`
				SearchLang string `json:"search_lang"`
				UILang     string `json:"ui_lang"`
				Safesearch string `json:"safesearch"`
				Spellcheck bool   `json:"spellcheck"`
			}{s.config.Providers.Brave.Search.Country, s.config.Providers.Brave.Search.SearchLang, s.config.Providers.Brave.Search.UILang, s.config.Providers.Brave.Search.Safesearch, s.config.Providers.Brave.Search.Spellcheck}
		}
		profile = append(profile, providerProfile{Name: name, Available: s.config.ProviderActionAvailable(name, string(provider.SEARCH)), Settings: settings})
	}
	return profile
}

func normalizeResults(results []Result) Response {
	normalizer := urlpolicy.New(nil)
	normalized := make([]Result, 0, len(results))
	for _, result := range results {
		parsedURL, err := normalizer.Normalize(result.URL)
		if err != nil {
			continue
		}
		result.NormalizedURL = parsedURL.String()
		result.Rank = len(normalized) + 1
		normalized = append(normalized, result)
	}
	return Response{Results: normalized}
}

func cacheResults(results []Result) []cache.SearchResult {
	cached := make([]cache.SearchResult, 0, len(results))
	for _, result := range results {
		cached = append(cached, cache.SearchResult{
			Rank:          result.Rank,
			URL:           result.URL,
			NormalizedURL: result.NormalizedURL,
			Title:         result.Title,
			Snippet:       result.Snippet,
			PublishedAt:   result.PublishedAt,
		})
	}
	return cached
}

func (s *Service) saveEmbeddedDocuments(ctx context.Context, results []Result, now time.Time) error {
	for _, result := range results {
		if result.EmbeddedContent == "" {
			continue
		}
		hash := sha256.Sum256([]byte(result.EmbeddedContent))
		_, err := s.cache.PutDocument(ctx, cache.Document{
			NormalizedURL: result.NormalizedURL,
			OriginalURL:   result.URL,
			Title:         result.Title,
			Content:       result.EmbeddedContent,
			Provider:      EXA_SEARCH,
			ContentHash:   hex.EncodeToString(hash[:]),
			FetchedAt:     now,
			ExpiresAt:     now.Add(s.config.Cache.DocumentTTL.Std()),
		})
		if err != nil {
			return fmt.Errorf("save embedded Exa document: %w", err)
		}
	}
	return nil
}

func entryFromCache(entry *cache.SearchEntry, cached bool) *Entry {
	return &Entry{
		ID:        entry.ID,
		Query:     entry.NormalizedQuery,
		Provider:  entry.Provider,
		Cached:    cached,
		CreatedAt: entry.CreatedAt,
		ExpiresAt: entry.ExpiresAt,
		Results:   entry.Results,
	}
}
