// Package authlogout implements POST /auth/logout — auth-internals.md §4
// "Token revocation": end the caller's own session immediately. Class A
// (auth-internals.md §9 "Route classes"): standard Host-header tenant
// resolution, standard JWT-or-API-key branch. The generic middleware
// pipeline that would normally run those two steps ahead of every Class A
// route doesn't exist yet (goerp#91, still blocked); this handler calls
// the same underlying primitives directly instead —
// tenantresolve.Resolver.ResolveByHost, then authcheck.Checker — the same
// "call the primitive directly, skip the not-yet-built generic
// middleware" pattern loginflow/mfareverify/mfaverify/authme already use.
// goerp#91/#224 will later lift this same logic into the automatic
// per-request pipeline; nothing here needs to be unwound when that
// happens.
package authlogout

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

type Handler struct {
	tenants *tenantresolve.Resolver
	auth    *authcheck.Checker
	revoker *sessionrevoke.Revoker
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, revoker *sessionrevoke.Revoker) *Handler {
	return &Handler{tenants: tenants, auth: auth, revoker: revoker}
}

// writeJSON matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own: '<', '>', '&' escaped for
// safe HTML embedding, and U+2028/U+2029 escaped for safe JS embedding.
func writeJSON(w http.ResponseWriter, v any) {
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeUnauthenticated(w http.ResponseWriter) {
	writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantCtx, err := h.tenants.ResolveByHost(ctx, r.Host)
	if err != nil {
		switch {
		case errors.Is(err, tenantresolve.ErrTenantNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		case errors.Is(err, tenantresolve.ErrTenantSuspended):
			writeJSONError(w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
		case errors.Is(err, tenantresolve.ErrTenantOffboarding):
			writeJSONError(w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
		default:
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "logout failed")
		}
		return
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeUnauthenticated(w)
		return
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		writeUnauthenticated(w)
		return
	}

	// An API key (authcheck.Checker.authenticateAPIKey) has no
	// SessionID — there is no session row to revoke, unlike JWT auth.
	if authCtx.AuthMethod == "api_key" {
		writeJSONError(w, http.StatusBadRequest, "api_key_no_session", "API key authentication has no session to log out of")
		return
	}

	// Revoke is idempotent (session.Store.Revoke's own documented
	// behavior) — a second logout for an already-revoked session succeeds
	// rather than erroring.
	if err := h.revoker.Revoke(ctx, authCtx.SessionID, "logout"); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "logout failed")
		return
	}

	if !loginsession.IsNonBrowser(r) {
		loginsession.ClearCookies(w)
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"ok": true})
}
