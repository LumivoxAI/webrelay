package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/LumivoxAI/webrelay/internal/provider"
	"github.com/LumivoxAI/webrelay/internal/search"
	"go.uber.org/zap"
)

// Dependencies contains optional business workflows used by HTTP routes.
type Dependencies struct {
	Search              *search.Service
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
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, CODE_DOCUMENT_NOT_FOUND, "endpoint not found")
	})
	return RequestID(Logging(logger, mux))
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
