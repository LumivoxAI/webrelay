// Package content implements cached document retrieval and pagination.
package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LumivoxAI/webrelay/internal/cache"
	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/pagination"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/LumivoxAI/webrelay/internal/urlpolicy"
	"go.uber.org/zap"
)

var (
	ErrInvalidRequest   = errors.New("invalid content request")
	ErrResultNotFound   = errors.New("search result not found")
	ErrDocumentNotFound = errors.New("document not found")
)

// Request identifies a document retrieval request.
type Request struct {
	URL          string
	ResultID     *string
	ForceRefresh bool
	Offset       *int
	Limit        *int
}

// ProviderResponse is normalized content returned by any upstream provider.
type ProviderResponse struct {
	URL             string
	Title           string
	Content         string
	SourceMediaType *string
}

// Client retrieves content through one provider action.
type Client interface {
	Fetch(context.Context, string, bool) (ProviderResponse, error)
}

// ClientFunc adapts a function to Client.
type ClientFunc func(context.Context, string, bool) (ProviderResponse, error)

func (f ClientFunc) Fetch(ctx context.Context, rawURL string, forceRefresh bool) (ProviderResponse, error) {
	return f(ctx, rawURL, forceRefresh)
}

// Response is a fully paginated document response for the HTTP layer.
type Response struct {
	Document       *cache.Document
	ResultID       *string
	SearchProvider *string
	Page           pagination.Page
	Cached         bool
}

// Service coordinates cache access, URL validation, provider routing and pagination.
type Service struct {
	cache     *cache.Store
	manager   *provider.Manager
	clients   map[provider.Key]Client
	providers []provider.Key
	config    config.Config
	policy    urlpolicy.Policy
	logger    *zap.Logger
	now       func() time.Time
}

// New creates a content workflow.
func New(cfg config.Config, store *cache.Store, manager *provider.Manager, clients map[provider.Key]Client, policy urlpolicy.Policy, logger *zap.Logger) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("document cache is required")
	}
	if manager == nil {
		return nil, fmt.Errorf("provider manager is required")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	providers := make([]provider.Key, 0, len(cfg.Content.Providers))
	for _, name := range cfg.Content.Providers {
		key, ok := contentKey(provider.Name(name))
		if !ok {
			return nil, fmt.Errorf("unsupported content provider %q", name)
		}
		providers = append(providers, key)
	}
	return &Service{cache: store, manager: manager, clients: clients, config: cfg, policy: policy, logger: logger, now: time.Now, providers: providers}, nil
}

func contentKey(name provider.Name) (provider.Key, bool) {
	switch name {
	case provider.TINYFISH:
		return provider.Key{Provider: name, Action: provider.FETCH}, true
	case provider.MARKDOWN_NEW:
		return provider.Key{Provider: name, Action: provider.FETCH}, true
	case provider.TAVILY:
		return provider.Key{Provider: name, Action: provider.EXTRACT}, true
	case provider.EXA:
		return provider.Key{Provider: name, Action: provider.CONTENTS}, true
	case provider.FIRECRAWL:
		return provider.Key{Provider: name, Action: provider.SCRAPE}, true
	default:
		return provider.Key{}, false
	}
}

// ReadResult retrieves content for a valid cached search result.
func (s *Service) ReadResult(ctx context.Context, resultID string, forceRefresh bool, offset, limit *int) (*Response, error) {
	result, err := s.cache.GetResult(ctx, resultID)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, ErrResultNotFound
	}
	searchProvider := result.SearchProvider
	return s.read(ctx, Request{URL: result.URL, ResultID: &result.ID, ForceRefresh: forceRefresh, Offset: offset, Limit: limit}, result.NormalizedURL, &searchProvider)
}

// Fetch retrieves content for an arbitrary public URL.
func (s *Service) Fetch(ctx context.Context, request Request) (*Response, error) {
	if strings.TrimSpace(request.URL) == "" {
		return nil, ErrInvalidRequest
	}
	return s.read(ctx, request, "", nil)
}

// ReadDocument returns a cached document by stable ID without provider routing.
func (s *Service) ReadDocument(ctx context.Context, documentID string, offset, limit *int) (*Response, error) {
	document, err := s.cache.GetDocument(ctx, documentID)
	if err != nil {
		return nil, err
	}
	if document == nil {
		return nil, ErrDocumentNotFound
	}
	return s.response(document, nil, nil, true, offset, limit)
}

func (s *Service) read(ctx context.Context, request Request, normalizedURL string, searchProvider *string) (*Response, error) {
	if !request.ForceRefresh && normalizedURL != "" {
		document, err := s.cache.GetDocumentByURL(ctx, normalizedURL)
		if err != nil {
			return nil, err
		}
		if document != nil {
			return s.response(document, request.ResultID, searchProvider, true, request.Offset, request.Limit)
		}
	}

	validatedURL, err := s.policy.Validate(ctx, request.URL)
	if err != nil {
		return nil, err
	}
	normalizedURL = validatedURL.String()
	if !request.ForceRefresh {
		document, err := s.cache.GetDocumentByURL(ctx, normalizedURL)
		if err != nil {
			return nil, err
		}
		if document != nil {
			return s.response(document, request.ResultID, searchProvider, true, request.Offset, request.Limit)
		}
	}

	var upstream ProviderResponse
	routed, err := s.manager.RouteKeys(ctx, s.providers, func(ctx context.Context, key provider.Key) error {
		client := s.clients[key]
		if client == nil {
			return &provider.Failure{Reason: provider.REASON_MISCONFIGURED}
		}
		response, err := client.Fetch(ctx, normalizedURL, request.ForceRefresh)
		if err != nil {
			return err
		}
		if strings.TrimSpace(response.Content) == "" {
			return &provider.Failure{Reason: provider.REASON_UNAVAILABLE}
		}
		upstream = response
		return nil
	})
	if err != nil {
		return nil, err
	}
	content := truncate(upstream.Content, s.config.Content.MaxDocumentChars)
	if len([]rune(content)) != len([]rune(upstream.Content)) {
		s.logger.Warn("Upstream content was truncated", zap.String("provider", string(routed.Provider)), zap.Int("max_document_chars", s.config.Content.MaxDocumentChars))
	}
	hash := sha256.Sum256([]byte(content))
	now := s.now().UTC()
	document, err := s.cache.PutDocument(ctx, cache.Document{
		NormalizedURL:   normalizedURL,
		OriginalURL:     request.URL,
		Title:           upstream.Title,
		Content:         content,
		Provider:        string(routed.Provider),
		SourceMediaType: upstream.SourceMediaType,
		ContentHash:     hex.EncodeToString(hash[:]),
		FetchedAt:       now,
		ExpiresAt:       now.Add(s.config.Cache.DocumentTTL.Std()),
	})
	if err != nil {
		return nil, err
	}
	return s.response(document, request.ResultID, searchProvider, false, request.Offset, request.Limit)
}

func (s *Service) response(document *cache.Document, resultID, searchProvider *string, cached bool, offset, limit *int) (*Response, error) {
	pageOffset := 0
	if offset != nil {
		pageOffset = *offset
	}
	pageLimit := s.config.Content.DefaultChunkChars
	if limit != nil {
		pageLimit = *limit
	}
	if pageLimit > s.config.Content.MaxChunkChars {
		return nil, ErrInvalidRequest
	}
	page, err := pagination.Slice(document.Content, pageOffset, pageLimit)
	if err != nil {
		return nil, err
	}
	return &Response{Document: document, ResultID: resultID, SearchProvider: searchProvider, Page: page, Cached: cached}, nil
}

func truncate(value string, limit int) string {
	characters := []rune(value)
	if len(characters) <= limit {
		return value
	}
	return string(characters[:limit])
}
