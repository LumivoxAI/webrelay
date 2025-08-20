package httpapi

import (
	"net/http"

	"go.uber.org/zap"
)

// NewHandler constructs the base HTTP pipeline. Business routes are added by later tasks.
func NewHandler(logger *zap.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, CodeDocumentNotFound, "endpoint not found")
	})
	return RequestID(Logging(logger, mux))
}
