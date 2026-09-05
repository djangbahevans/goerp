// Package authme implements GET /auth/me — the check the shell's client-
// side auth state machine (shell-architecture.md §7) runs on mount to
// find out whether an existing session is still valid, without a login
// form. Class A (auth-internals.md §9 "Route classes"): standard Host-
// header tenant resolution, standard JWT-or-API-key branch. The generic
// middleware pipeline that would normally run those two steps ahead of
// every Class A route doesn't exist yet (goerp#91, still blocked); this
// handler calls the same underlying primitives directly instead —
// tenantresolve.Resolver.ResolveByHost, then authcheck.Checker — the same
// "call the primitive directly, skip the not-yet-built generic
// middleware" pattern loginflow/mfareverify/mfaverify already use.
// goerp#91/#224 will later lift this same logic into the automatic
// per-request pipeline; nothing here needs to be unwound when that
// happens.
package authme

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

type Handler struct {
	tenants *tenantresolve.Resolver
	auth    *authcheck.Checker
	users   *user.Store
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, users *user.Store) *Handler {
	return &Handler{tenants: tenants, auth: auth, users: users}
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

type meResponse struct {
	User   meUser   `json:"user"`
	Tenant meTenant `json:"tenant"`
}

// meUser deliberately omits name/avatarUrl/locale/timezone
// (typescript-sdk-reference.md §6's CurrentUser) — user.User has no
// backing columns for them yet (a separate, unfiled user-profile-fields
// ticket). Every field below already exists on user.User/AuthContext.
type meUser struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	Roles         []string   `json:"roles"`
	AMR           []string   `json:"amr"`
	MFAVerifiedAt *time.Time `json:"mfa_verified_at"`
}

// meTenant deliberately omits logoUrl/locale/timezone/currency
// (CurrentTenant) — tenant.Tenant has no backing columns for them yet
// (a separate, unfiled tenant-locale-settings ticket).
type meTenant struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Plan string `json:"plan"`
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
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "session check failed")
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

	u, err := h.users.GetByID(ctx, authCtx.UserID)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			writeUnauthenticated(w)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "session check failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, meResponse{
		User: meUser{
			ID:            authCtx.UserID,
			Email:         u.Email,
			Roles:         authCtx.RolesLive,
			AMR:           authCtx.AMR,
			MFAVerifiedAt: authCtx.MFAVerifiedAt,
		},
		Tenant: meTenant{
			ID:   tenantCtx.TenantID,
			Slug: tenantCtx.Slug,
			Name: tenantCtx.Name,
			Plan: string(tenantCtx.Plan),
		},
	})
}
