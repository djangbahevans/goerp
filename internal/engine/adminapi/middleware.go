package adminapi

import (
	"crypto/hmac"
	"net/http"
	"strings"
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
