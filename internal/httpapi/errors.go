package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
)

// Code is a stable machine-readable error code exposed by the API.
type Code string

const (
	CODE_INVALID_REQUEST        Code = "invalid_request"
	CODE_INVALID_QUERY          Code = "invalid_query"
	CODE_INVALID_URL            Code = "invalid_url"
	CODE_UNSUPPORTED_URL        Code = "unsupported_url"
	CODE_RESULT_NOT_FOUND       Code = "result_not_found"
	CODE_DOCUMENT_NOT_FOUND     Code = "document_not_found"
	CODE_RANGE_NOT_SATISFIABLE  Code = "range_not_satisfiable"
	CODE_PROVIDER_MISCONFIGURED Code = "provider_misconfigured"
	CODE_PROVIDER_UNAUTHORIZED  Code = "provider_unauthorized"
	CODE_PROVIDER_FORBIDDEN     Code = "provider_forbidden"
	CODE_QUOTA_EXHAUSTED        Code = "quota_exhausted"
	CODE_RATE_LIMITED           Code = "rate_limited"
	CODE_UPSTREAM_TIMEOUT       Code = "upstream_timeout"
	CODE_TEMPORARY_FAILURE      Code = "temporary_failure"
	CODE_CONTENT_UNAVAILABLE    Code = "content_unavailable"
	CODE_ALL_PROVIDERS_FAILED   Code = "all_providers_failed"
	CODE_SERVICE_UNAVAILABLE    Code = "service_unavailable"
	CODE_INTERNAL               Code = "internal_error"
)

// HTTPStatus returns the contract-defined HTTP status for an API error code.
func (c Code) HTTPStatus() int {
	switch c {
	case CODE_INVALID_REQUEST, CODE_INVALID_QUERY, CODE_INVALID_URL:
		return http.StatusBadRequest
	case CODE_RESULT_NOT_FOUND, CODE_DOCUMENT_NOT_FOUND:
		return http.StatusNotFound
	case CODE_RANGE_NOT_SATISFIABLE:
		return http.StatusRequestedRangeNotSatisfiable
	case CODE_UNSUPPORTED_URL, CODE_CONTENT_UNAVAILABLE:
		return http.StatusUnprocessableEntity
	case CODE_RATE_LIMITED:
		return http.StatusTooManyRequests
	case CODE_UPSTREAM_TIMEOUT:
		return http.StatusGatewayTimeout
	case CODE_SERVICE_UNAVAILABLE, CODE_PROVIDER_MISCONFIGURED:
		return http.StatusServiceUnavailable
	case CODE_INTERNAL:
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
