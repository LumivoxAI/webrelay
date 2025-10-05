package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"time"

	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

const MAX_REQUEST_ID_LENGTH = 128

var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type requestIDContextKey struct{}

// RequestID assigns a safe request ID and returns it in every response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if !isSafeRequestID(requestID) {
			requestID = ulid.Make().String()
		}

		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isSafeRequestID(requestID string) bool {
	return len(requestID) <= MAX_REQUEST_ID_LENGTH && safeRequestID.MatchString(requestID)
}

// Logging emits request metadata without request bodies or response content.
func Logging(logger *zap.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("HTTP request completed",
			zap.String("request_id", RequestIDFromContext(r.Context())),
			zap.String("operation", r.Method+" "+r.URL.Path),
			zap.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
		)
	})
}
