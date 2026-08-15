package adminapi

import (
	"context"
	"crypto/hmac"
	"net/http"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/auditlog"
	"github.com/rs/zerolog/log"
)

// adminAuthMiddleware gates every admin route behind the single shared
// GOERP_ADMIN_TOKEN (engine-internals.md §11 "Admin API authentication").
// Distinct, individually-revocable operator identity is a separate layer
// (the admin gateway's mTLS certs) this token doesn't provide on its own.
func adminAuthMiddleware(adminToken string) func(http.Handler) http.Handler {
	expected := []byte(adminToken)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !hmac.Equal([]byte(bearer), expected) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func auditLogMiddleware(store *auditlog.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				next.ServeHTTP(w, r)
				return
			}

			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			row := auditlog.Row{
				OperatorIdentity: operatorIdentity(r),
				// r.Pattern is the matched ServeMux pattern, which already
				// includes the method ("POST /admin/tenants/{slug}/suspend")
				// since every route in this package registers with an
				// explicit method prefix — prepending r.Method again would
				// double it up.
				Endpoint:       r.Pattern,
				TargetScope:    targetScope(r),
				IdempotencyKey: r.Header.Get("Idempotency-Key"),
				JobID:          jobIDFromResponse(rec.body, rec.status),
				StatusCode:     rec.status,
			}
			if err := store.Write(context.WithoutCancel(r.Context()), row); err != nil {
				log.Error().Err(err).Str("endpoint", row.Endpoint).Msg("admin api: write audit row")
			}
		})
	}
}
