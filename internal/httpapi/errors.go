package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
)

// Code is a stable machine-readable error code exposed by the API.
type Code string

const (
	CodeInvalidRequest        Code = "invalid_request"
	CodeInvalidQuery          Code = "invalid_query"
	CodeInvalidURL            Code = "invalid_url"
	CodeUnsupportedURL        Code = "unsupported_url"
	CodeResultNotFound        Code = "result_not_found"
	CodeDocumentNotFound      Code = "document_not_found"
	CodeRangeNotSatisfiable   Code = "range_not_satisfiable"
	CodeProviderMisconfigured Code = "provider_misconfigured"
	CodeProviderUnauthorized  Code = "provider_unauthorized"
	CodeProviderForbidden     Code = "provider_forbidden"
	CodeQuotaExhausted        Code = "quota_exhausted"
	CodeRateLimited           Code = "rate_limited"
	CodeUpstreamTimeout       Code = "upstream_timeout"
	CodeTemporaryFailure      Code = "temporary_failure"
	CodeContentUnavailable    Code = "content_unavailable"
	CodeAllProvidersFailed    Code = "all_providers_failed"
	CodeServiceUnavailable    Code = "service_unavailable"
	CodeInternal              Code = "internal_error"
)

// HTTPStatus returns the contract-defined HTTP status for an API error code.
func (c Code) HTTPStatus() int {
	switch c {
	case CodeInvalidRequest, CodeInvalidQuery, CodeInvalidURL:
		return http.StatusBadRequest
	case CodeResultNotFound, CodeDocumentNotFound:
		return http.StatusNotFound
	case CodeRangeNotSatisfiable:
		return http.StatusRequestedRangeNotSatisfiable
	case CodeUnsupportedURL, CodeContentUnavailable:
		return http.StatusUnprocessableEntity
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeUpstreamTimeout:
		return http.StatusGatewayTimeout
	case CodeServiceUnavailable, CodeProviderMisconfigured:
		return http.StatusServiceUnavailable
	case CodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusBadGateway
	}
}

// RequestIDFromContext returns the ID assigned by RequestID middleware.
func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

// WriteError sends a contract-compliant JSON API error.
func WriteError(w http.ResponseWriter, r *http.Request, code Code, message string, attempts ...Attempt) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code.HTTPStatus())
	_ = json.NewEncoder(w).Encode(errorResponse{Error: Error{
		Code:      code,
		Message:   message,
		RequestID: RequestIDFromContext(r.Context()),
		Attempts:  attempts,
	}})
}
