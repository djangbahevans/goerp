package adminapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// ctxKeyBody is the context key bodyCapMiddleware stashes the buffered
// request body under, so auditLogMiddleware's targetScope can read a
// best-effort "slug" field out of it without a second read of r.Body
// (already replaced with a fresh reader by the time audit runs).
type ctxKey int

const ctxKeyBody ctxKey = iota

func bodyCapMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
			if err != nil {
				writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds limit")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyBody, body)))
		})
	}
}

func concurrencyLimitMiddleware(max int) func(http.Handler) http.Handler {
	sem := make(chan struct{}, max)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				next.ServeHTTP(w, r)
			default:
				writeError(w, http.StatusServiceUnavailable, "admin_api_busy", "too many concurrent admin requests")
			}
		})
	}
}
