package gateway

import (
	"crypto/hmac"
	"encoding/json/v2"
	"net/http"
	"strings"
)

// Distinguishes a gateway-side rejection from the admin API's own
// "unauthorized" — both would otherwise be a superficially identical 401.
const gatewayAuthFailedCode = "gateway_auth_failed"

// bearerTokenMiddleware checks the same shared GOERP_ADMIN_TOKEN the
// admin API checks, constant-time. Not shared code with adminapi's own
// version — separate binary, not worth the cross-package coupling.
func bearerTokenMiddleware(adminToken string) func(http.Handler) http.Handler {
	expected := []byte(adminToken)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !hmac.Equal([]byte(bearer), expected) {
				writeAuthError(w, http.StatusUnauthorized, gatewayAuthFailedCode, "invalid or missing bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type authErrorEnvelope struct {
	Error authErrorBody `json:"error"`
}

type authErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Matches the admin API's own {"error":{"code","message"}} envelope shape.
func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, authErrorEnvelope{Error: authErrorBody{Code: code, Message: message}})
}
