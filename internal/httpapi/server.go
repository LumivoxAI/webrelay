package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/LumivoxAI/webrelay/internal/content"
	"github.com/LumivoxAI/webrelay/internal/pagination"
	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/LumivoxAI/webrelay/internal/search"
	"github.com/LumivoxAI/webrelay/internal/urlpolicy"
	"go.uber.org/zap"
)

// Dependencies contains optional business workflows used by HTTP routes.
type Dependencies struct {
	Search              *search.Service
	Content             *content.Service
	MaxRequestBodyBytes int64
}

// NewHandler constructs the public HTTP pipeline.
func NewHandler(logger *zap.Logger, dependencies ...Dependencies) http.Handler {
	settings := Dependencies{MaxRequestBodyBytes: 1 << 20}
	if len(dependencies) > 0 {
		settings = dependencies[0]
		if settings.MaxRequestBodyBytes <= 0 {
			settings.MaxRequestBodyBytes = 1 << 20
		}
	}
	mux := http.NewServeMux()
	if settings.Search != nil {
		mux.HandleFunc("/v1/search", searchHandler(settings))
	}
	if settings.Content != nil {
		mux.HandleFunc("/v1/fetch", fetchHandler(settings))
		mux.HandleFunc("/v1/results/", resultContentHandler(settings))
		mux.HandleFunc("/v1/documents/", documentContentHandler(settings))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, CODE_DOCUMENT_NOT_FOUND, "endpoint not found")
	})
	return RequestID(Logging(logger, mux))
}

func fetchHandler(dependencies Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, r, CODE_INVALID_REQUEST, "method must be POST")
			return
		}
		var request FetchRequest
		if apiError := DecodeJSON(w, r, &request, dependencies.MaxRequestBodyBytes); apiError != nil {
			WriteError(w, r, apiError.Code, apiError.Message)
			return
		}
		response, err := dependencies.Content.Fetch(r.Context(), content.Request{URL: request.URL, ForceRefresh: request.ForceRefresh})
		if err != nil {
			writeContentError(w, r, err)
			return
		}
		writeContentResponse(w, response)
	}
}

func resultContentHandler(dependencies Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, CODE_INVALID_REQUEST, "method must be GET")
			return
		}
		resultID, ok := resourceID(r.URL.Path, "/v1/results/", "/content")
		if !ok {
			WriteError(w, r, CODE_RESULT_NOT_FOUND, "search result not found")
			return
		}
		offset, limit, forceRefresh, err := contentParameters(r)
		if err != nil {
			WriteError(w, r, CODE_INVALID_REQUEST, "content parameters are invalid")
			return
		}
		response, err := dependencies.Content.ReadResult(r.Context(), resultID, forceRefresh, offset, limit)
		if err != nil {
			writeContentError(w, r, err)
			return
		}
		writeContentResponse(w, response)
	}
}

func documentContentHandler(dependencies Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, CODE_INVALID_REQUEST, "method must be GET")
			return
		}
		documentID, ok := resourceID(r.URL.Path, "/v1/documents/", "/content")
		if !ok {
			WriteError(w, r, CODE_DOCUMENT_NOT_FOUND, "document not found")
			return
		}
		offset, limit, _, err := contentParameters(r)
		if err != nil {
			WriteError(w, r, CODE_INVALID_REQUEST, "content parameters are invalid")
			return
		}
		response, err := dependencies.Content.ReadDocument(r.Context(), documentID, offset, limit)
		if err != nil {
			writeContentError(w, r, err)
			return
		}
		writeContentResponse(w, response)
	}
}

func resourceID(path, prefix, suffix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return id, id != "" && !strings.Contains(id, "/")
}

func contentParameters(r *http.Request) (*int, *int, bool, error) {
	query := r.URL.Query()
	for name := range query {
		if name != "offset" && name != "limit" && name != "force_refresh" {
			return nil, nil, false, errors.New("unknown parameter")
		}
	}
	offset, err := integerParameter(query.Get("offset"))
	if err != nil {
		return nil, nil, false, err
	}
	limit, err := integerParameter(query.Get("limit"))
	if err != nil {
		return nil, nil, false, err
	}
	forceRefresh := false
	if raw := query.Get("force_refresh"); raw != "" {
		forceRefresh, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, nil, false, err
		}
	}
	return offset, limit, forceRefresh, nil
}

func integerParameter(raw string) (*int, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func writeContentResponse(w http.ResponseWriter, response *content.Response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(ContentResponse{
		DocumentID: response.Document.ID, ResultID: response.ResultID, URL: response.Document.OriginalURL, Title: response.Document.Title,
		Content: response.Page.Content, ContentIsUntrusted: true, Offset: response.Page.Offset, ReturnedChars: response.Page.ReturnedChars,
		TotalChars: response.Page.TotalChars, Truncated: response.Page.Truncated, NextOffset: response.Page.NextOffset, Cached: response.Cached,
		SearchProvider: response.SearchProvider, ContentProvider: response.Document.Provider, FetchedAt: response.Document.FetchedAt, ExpiresAt: response.Document.ExpiresAt,
	})
}

func writeContentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, content.ErrInvalidRequest), errors.Is(err, pagination.ErrInvalidOffset), errors.Is(err, pagination.ErrInvalidLimit):
		WriteError(w, r, CODE_INVALID_REQUEST, "content request is invalid")
	case errors.Is(err, content.ErrResultNotFound):
		WriteError(w, r, CODE_RESULT_NOT_FOUND, "search result not found")
	case errors.Is(err, content.ErrDocumentNotFound):
		WriteError(w, r, CODE_DOCUMENT_NOT_FOUND, "document not found")
	case errors.Is(err, pagination.ErrOutOfRange):
		WriteError(w, r, CODE_RANGE_NOT_SATISFIABLE, "content offset exceeds document length")
	case errors.Is(err, urlpolicy.ErrInvalidURL):
		WriteError(w, r, CODE_INVALID_URL, "URL is invalid")
	case errors.Is(err, urlpolicy.ErrUnsupportedURL):
		WriteError(w, r, CODE_UNSUPPORTED_URL, "URL is unsupported")
	default:
		var routeError *provider.RouteError
		if !errors.As(err, &routeError) {
			WriteError(w, r, CODE_INTERNAL, "internal server error")
			return
		}
		attempts := make([]Attempt, 0, len(routeError.Attempts))
		for _, attempt := range routeError.Attempts {
			attempts = append(attempts, Attempt{Provider: string(attempt.Provider), Reason: string(attempt.Reason)})
		}
		WriteError(w, r, codeForReason(routeError.Code()), "content providers failed", attempts...)
	}
}

func searchHandler(dependencies Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, r, CODE_INVALID_REQUEST, "method must be POST")
			return
		}
		var request SearchRequest
		if apiError := DecodeJSON(w, r, &request, dependencies.MaxRequestBodyBytes); apiError != nil {
			WriteError(w, r, apiError.Code, apiError.Message)
			return
		}
		entry, err := dependencies.Search.Search(r.Context(), search.Request{
			Query:           request.Query,
			Limit:           request.Limit,
			IncludeDomains:  request.IncludeDomains,
			ExcludeDomains:  request.ExcludeDomains,
			PublishedAfter:  request.PublishedAfter,
			PublishedBefore: request.PublishedBefore,
			ForceRefresh:    request.ForceRefresh,
		})
		if err != nil {
			writeSearchError(w, r, err)
			return
		}
		response := SearchResponse{
			SearchID:  entry.ID,
			Query:     entry.Query,
			Provider:  entry.Provider,
			Cached:    entry.Cached,
			CreatedAt: entry.CreatedAt,
			ExpiresAt: entry.ExpiresAt,
			Results:   make([]SearchResult, 0, len(entry.Results)),
		}
		for _, result := range entry.Results {
			response.Results = append(response.Results, SearchResult{
				ID:          result.ID,
				Rank:        result.Rank,
				Title:       result.Title,
				URL:         result.URL,
				Snippet:     result.Snippet,
				PublishedAt: result.PublishedAt,
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(response)
	}
}

func writeSearchError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, search.ErrInvalidQuery):
		WriteError(w, r, CODE_INVALID_QUERY, "query is invalid")
	case errors.Is(err, search.ErrInvalidRequest):
		WriteError(w, r, CODE_INVALID_REQUEST, "search request is invalid")
	default:
		var routeError *provider.RouteError
		if !errors.As(err, &routeError) {
			WriteError(w, r, CODE_INTERNAL, "internal server error")
			return
		}
		attempts := make([]Attempt, 0, len(routeError.Attempts))
		for _, attempt := range routeError.Attempts {
			attempts = append(attempts, Attempt{Provider: string(attempt.Provider), Reason: string(attempt.Reason)})
		}
		WriteError(w, r, codeForReason(routeError.Code()), "search providers failed", attempts...)
	}
}

func codeForReason(reason provider.Reason) Code {
	switch reason {
	case provider.REASON_MISCONFIGURED:
		return CODE_PROVIDER_MISCONFIGURED
	case provider.REASON_UNAUTHORIZED:
		return CODE_PROVIDER_UNAUTHORIZED
	case provider.REASON_FORBIDDEN:
		return CODE_PROVIDER_FORBIDDEN
	case provider.REASON_QUOTA:
		return CODE_QUOTA_EXHAUSTED
	case provider.REASON_RATE_LIMITED:
		return CODE_RATE_LIMITED
	case provider.REASON_TIMEOUT:
		return CODE_UPSTREAM_TIMEOUT
	case provider.REASON_UNAVAILABLE:
		return CODE_CONTENT_UNAVAILABLE
	case provider.REASON_TEMPORARY:
		return CODE_TEMPORARY_FAILURE
	default:
		return CODE_ALL_PROVIDERS_FAILED
	}
}
