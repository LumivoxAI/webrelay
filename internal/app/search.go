package app

import (
	"context"
	"fmt"

	"github.com/LumivoxAI/webrelay/internal/brave"
	"github.com/LumivoxAI/webrelay/internal/cache"
	"github.com/LumivoxAI/webrelay/internal/config"
	"github.com/LumivoxAI/webrelay/internal/content"
	"github.com/LumivoxAI/webrelay/internal/exa"
	"github.com/LumivoxAI/webrelay/internal/firecrawl"
	"github.com/LumivoxAI/webrelay/internal/markdownnew"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/LumivoxAI/webrelay/internal/search"
	"github.com/LumivoxAI/webrelay/internal/tavily"
	"github.com/LumivoxAI/webrelay/internal/tinyfish"
	"github.com/LumivoxAI/webrelay/internal/urlpolicy"
	"go.uber.org/zap"
)

// NewSearchWorkflow wires all configured search adapters into the shared workflow.
func NewSearchWorkflow(cfg config.Config, store *cache.Store, logger *zap.Logger) (*search.Service, error) {
	workflow, _, err := NewWorkflows(cfg, store, logger)
	return workflow, err
}

// NewWorkflows wires search and content adapters to one shared provider manager.
func NewWorkflows(cfg config.Config, store *cache.Store, logger *zap.Logger) (*search.Service, *content.Service, error) {
	manager := provider.NewConfiguredManager(cfg, logger)
	httpClients, err := provider.NewConfiguredClients(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("create provider HTTP clients: %w", err)
	}
	exaClient, err := exa.New(cfg.Providers.Exa, httpClients[provider.Key{Provider: provider.EXA, Action: provider.SEARCH}], logger)
	if err != nil {
		return nil, nil, err
	}
	braveClient, err := brave.New(cfg.Providers.Brave, httpClients[provider.Key{Provider: provider.BRAVE, Action: provider.SEARCH}], logger)
	if err != nil {
		return nil, nil, err
	}
	tinyFishClient, err := tinyfish.New(cfg.Providers.TinyFish, httpClients[provider.Key{Provider: provider.TINYFISH, Action: provider.SEARCH}], httpClients[provider.Key{Provider: provider.TINYFISH, Action: provider.FETCH}], logger)
	if err != nil {
		return nil, nil, err
	}
	tavilyClient, err := tavily.New(cfg.Providers.Tavily, httpClients[provider.Key{Provider: provider.TAVILY, Action: provider.SEARCH}], httpClients[provider.Key{Provider: provider.TAVILY, Action: provider.EXTRACT}], manager.Metrics(), logger)
	if err != nil {
		return nil, nil, err
	}
	firecrawlClient, err := firecrawl.New(cfg.Providers.Firecrawl, httpClients[provider.Key{Provider: provider.FIRECRAWL, Action: provider.SEARCH}], httpClients[provider.Key{Provider: provider.FIRECRAWL, Action: provider.SCRAPE}], manager.Metrics(), logger)
	if err != nil {
		return nil, nil, err
	}
	markdownClient, err := markdownnew.New(cfg.Providers.MarkdownNew, httpClients[provider.Key{Provider: provider.MARKDOWN_NEW, Action: provider.FETCH}], logger)
	if err != nil {
		return nil, nil, err
	}
	clients := map[provider.Name]search.Client{
		provider.EXA:       search.ClientFunc(searchExa(exaClient)),
		provider.TINYFISH:  search.ClientFunc(searchTinyFish(tinyFishClient)),
		provider.TAVILY:    search.ClientFunc(searchTavily(tavilyClient)),
		provider.FIRECRAWL: search.ClientFunc(searchFirecrawl(firecrawlClient)),
		provider.BRAVE:     search.ClientFunc(searchBrave(braveClient)),
	}
	searchWorkflow, err := search.New(cfg, store, manager, clients, logger)
	if err != nil {
		return nil, nil, err
	}
	contentClients := map[provider.Key]content.Client{
		{Provider: provider.TINYFISH, Action: provider.FETCH}: content.ClientFunc(func(ctx context.Context, rawURL string, forceRefresh bool) (content.ProviderResponse, error) {
			response, err := tinyFishClient.Fetch(ctx, tinyfish.ContentRequest{URL: rawURL, DocumentTTL: cfg.Cache.DocumentTTL.Std(), ForceRefresh: forceRefresh})
			return content.ProviderResponse{URL: response.URL, Title: response.Title, Content: response.Content}, err
		}),
		{Provider: provider.MARKDOWN_NEW, Action: provider.FETCH}: content.ClientFunc(func(ctx context.Context, rawURL string, _ bool) (content.ProviderResponse, error) {
			response, err := markdownClient.Fetch(ctx, markdownnew.ContentRequest{URL: rawURL})
			return content.ProviderResponse{Content: response.Content, SourceMediaType: response.SourceMediaType}, err
		}),
		{Provider: provider.TAVILY, Action: provider.EXTRACT}: content.ClientFunc(func(ctx context.Context, rawURL string, _ bool) (content.ProviderResponse, error) {
			response, err := tavilyClient.Extract(ctx, tavily.ContentRequest{URL: rawURL})
			return content.ProviderResponse{URL: response.URL, Content: response.Content}, err
		}),
		{Provider: provider.EXA, Action: provider.CONTENTS}: content.ClientFunc(func(ctx context.Context, rawURL string, _ bool) (content.ProviderResponse, error) {
			response, err := exaClient.Contents(ctx, exa.ContentRequest{URL: rawURL})
			return content.ProviderResponse{URL: response.URL, Title: response.Title, Content: response.Content, SourceMediaType: response.SourceMediaType}, err
		}),
		{Provider: provider.FIRECRAWL, Action: provider.SCRAPE}: content.ClientFunc(func(ctx context.Context, rawURL string, forceRefresh bool) (content.ProviderResponse, error) {
			response, err := firecrawlClient.Scrape(ctx, firecrawl.ContentRequest{URL: rawURL, DocumentTTL: cfg.Cache.DocumentTTL.Std(), ForceRefresh: forceRefresh})
			return content.ProviderResponse{URL: response.URL, Title: response.Title, Content: response.Content}, err
		}),
	}
	contentWorkflow, err := content.New(cfg, store, manager, contentClients, urlpolicy.New(nil), logger)
	if err != nil {
		return nil, nil, err
	}
	return searchWorkflow, contentWorkflow, nil
}

func searchExa(client *exa.Client) func(context.Context, search.Request) (search.Response, error) {
	return func(ctx context.Context, request search.Request) (search.Response, error) {
		response, err := client.Search(ctx, exa.SearchRequest{Query: request.Query, Limit: *request.Limit, IncludeDomains: request.IncludeDomains, ExcludeDomains: request.ExcludeDomains, PublishedAfter: request.PublishedAfter, PublishedBefore: request.PublishedBefore})
		if err != nil {
			return search.Response{}, err
		}
		results := make([]search.Result, 0, len(response.Results))
		for _, result := range response.Results {
			results = append(results, search.Result{Rank: result.Rank, URL: result.URL, Title: result.Title, Snippet: result.Snippet, PublishedAt: result.PublishedAt, EmbeddedContent: result.EmbeddedContent})
		}
		return search.Response{Results: results}, nil
	}
}

func searchTinyFish(client *tinyfish.Client) func(context.Context, search.Request) (search.Response, error) {
	return func(ctx context.Context, request search.Request) (search.Response, error) {
		response, err := client.Search(ctx, tinyfish.SearchRequest{Query: request.Query, Limit: *request.Limit, IncludeDomains: request.IncludeDomains, ExcludeDomains: request.ExcludeDomains, PublishedAfter: request.PublishedAfter, PublishedBefore: request.PublishedBefore})
		if err != nil {
			return search.Response{}, err
		}
		results := make([]search.Result, 0, len(response.Results))
		for _, result := range response.Results {
			results = append(results, search.Result{Rank: result.Rank, URL: result.URL, Title: result.Title, Snippet: result.Snippet, PublishedAt: result.PublishedAt})
		}
		return search.Response{Results: results}, nil
	}
}

func searchTavily(client *tavily.Client) func(context.Context, search.Request) (search.Response, error) {
	return func(ctx context.Context, request search.Request) (search.Response, error) {
		response, err := client.Search(ctx, tavily.SearchRequest{Query: request.Query, Limit: *request.Limit, IncludeDomains: request.IncludeDomains, ExcludeDomains: request.ExcludeDomains, PublishedAfter: request.PublishedAfter, PublishedBefore: request.PublishedBefore})
		if err != nil {
			return search.Response{}, err
		}
		results := make([]search.Result, 0, len(response.Results))
		for _, result := range response.Results {
			results = append(results, search.Result{Rank: result.Rank, URL: result.URL, Title: result.Title, Snippet: result.Snippet, PublishedAt: result.PublishedAt})
		}
		return search.Response{Results: results}, nil
	}
}

func searchFirecrawl(client *firecrawl.Client) func(context.Context, search.Request) (search.Response, error) {
	return func(ctx context.Context, request search.Request) (search.Response, error) {
		response, err := client.Search(ctx, firecrawl.SearchRequest{Query: request.Query, Limit: *request.Limit, IncludeDomains: request.IncludeDomains, ExcludeDomains: request.ExcludeDomains, PublishedAfter: request.PublishedAfter, PublishedBefore: request.PublishedBefore})
		if err != nil {
			return search.Response{}, err
		}
		results := make([]search.Result, 0, len(response.Results))
		for _, result := range response.Results {
			results = append(results, search.Result{Rank: result.Rank, URL: result.URL, Title: result.Title, Snippet: result.Snippet})
		}
		return search.Response{Results: results}, nil
	}
}

func searchBrave(client *brave.Client) func(context.Context, search.Request) (search.Response, error) {
	return func(ctx context.Context, request search.Request) (search.Response, error) {
		response, err := client.Search(ctx, brave.SearchRequest{Query: request.Query, Limit: *request.Limit, IncludeDomains: request.IncludeDomains, ExcludeDomains: request.ExcludeDomains, PublishedAfter: request.PublishedAfter, PublishedBefore: request.PublishedBefore})
		if err != nil {
			return search.Response{}, err
		}
		results := make([]search.Result, 0, len(response.Results))
		for _, result := range response.Results {
			results = append(results, search.Result{Rank: result.Rank, URL: result.URL, Title: result.Title, Snippet: result.Snippet, PublishedAt: result.PublishedAt})
		}
		return search.Response{Results: results}, nil
	}
}
