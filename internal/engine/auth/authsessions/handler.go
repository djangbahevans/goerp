// Package authsessions implements a user's own session list and revoke
// endpoints — GET /auth/sessions, DELETE /auth/sessions/{family_id} and
// DELETE /auth/sessions (auth-internals.md §4 "Session management
// endpoints"). One login is one session family; these routes list and end
// families in the resolved tenant only. Class A routes that resolve the
// tenant from Host and authenticate the access token themselves, like
// internal/engine/auth/authlogout.
package authsessions

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"time"
	"uuid"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

const revokeReason = "logout"

type Handler struct {
	tenants  *tenantresolve.Resolver
	auth     *authcheck.Checker
	sessions *session.Store
	revoker  *sessionrevoke.Revoker
	audit    *authaudit.Store
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, sessions *session.Store, revoker *sessionrevoke.Revoker, audit *authaudit.Store) *Handler {
	return &Handler{tenants: tenants, auth: auth, sessions: sessions, revoker: revoker, audit: audit}
}

type sessionJSON struct {
	ID           string    `json:"id"`
	UserAgent    *string   `json:"user_agent"`
	IPAddress    *string   `json:"ip_address"`
	CountryCode  *string   `json:"country_code"`
	SignedInAt   time.Time `json:"signed_in_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	Persistent   bool      `json:"persistent"`
	Current      bool      `json:"current"`
}

// writeJSON matches encoding/json v1's Encoder defaults, the same as
// authlogout's own writeJSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeInternalError(w http.ResponseWriter, err error, msg string) {
	log.Error().Err(err).Msg("authsessions: " + msg)
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
}

type caller struct {
	tenant *tenantresolve.TenantContext
	auth   *authcheck.AuthContext
	// family is the caller's own session family.
	family string
}

// authenticate resolves the tenant from Host, authenticates the access
// token and loads the caller's session family. An API key has no session
// to list or keep, so it is rejected the way POST /auth/logout rejects it.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (caller, bool) {
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
			writeInternalError(w, err, "tenant resolution failed")
		}
		return caller{}, false
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return caller{}, false
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return caller{}, false
	}
	if authCtx.AuthMethod == "api_key" {
		writeJSONError(w, http.StatusBadRequest, "api_key_no_session", "API key authentication has no session")
		return caller{}, false
	}

	family, err := h.sessions.FamilyIDForSession(ctx, authCtx.SessionID)
	if err != nil {
		writeInternalError(w, err, "current session lookup failed")
		return caller{}, false
	}
	return caller{tenant: tenantCtx, auth: authCtx, family: family}, true
}

// ServeList is GET /auth/sessions: the caller's live session families in
// this tenant, the current one first, then most recently active first.
func (h *Handler) ServeList(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	families, err := h.sessions.LiveFamiliesForUserInTenant(r.Context(), c.auth.UserID, c.tenant.TenantID)
	if err != nil {
		writeInternalError(w, err, "list sessions failed")
		return
	}
	out := make([]sessionJSON, len(families))
	for i, f := range families {
		out[i] = sessionJSON{
			ID:           f.ID,
			UserAgent:    f.UserAgent,
			IPAddress:    f.IPAddress,
			CountryCode:  f.CountryCode,
			SignedInAt:   f.SignedInAt,
			LastActiveAt: f.LastActiveAt,
			Persistent:   f.Persistent,
			Current:      f.ID == c.family,
		}
	}
	// Stable, so the current session moves first and the rest keep the
	// store's most-recently-active order.
	slices.SortStableFunc(out, func(a, b sessionJSON) int {
		return cmp.Compare(boolRank(b.Current), boolRank(a.Current))
	})
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ServeRevoke is DELETE /auth/sessions/{family_id}: ends one of the
// caller's other sessions in this tenant.
func (h *Handler) ServeRevoke(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	familyID := route.ParamsFromContext(ctx)["family_id"]
	if _, err := uuid.Parse(familyID); err != nil {
		writeJSONError(w, http.StatusNotFound, "session_not_found", "session not found")
		return
	}
	family, err := h.sessions.LiveFamilyForUserInTenant(ctx, c.auth.UserID, c.tenant.TenantID, familyID)
	if errors.Is(err, session.ErrSessionNotFound) {
		writeJSONError(w, http.StatusNotFound, "session_not_found", "session not found")
		return
	}
	if err != nil {
		writeInternalError(w, err, "session lookup failed")
		return
	}
	if family.ID == c.family {
		writeJSONError(w, http.StatusBadRequest, "cannot_revoke_current_session", "sign out to end the current session")
		return
	}

	if err := h.revoker.RevokeFamily(ctx, family.ID, revokeReason); err != nil {
		writeInternalError(w, err, "session revocation failed")
		return
	}
	h.recordSessionRevoked(r, c, family.ID, family.LiveRowID)
	w.WriteHeader(http.StatusNoContent)
}

// ServeRevokeOthers is DELETE /auth/sessions: ends every session of the
// caller's in this tenant except the current one.
func (h *Handler) ServeRevokeOthers(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	families, err := h.revoker.RevokeOtherFamiliesForUserInTenant(r.Context(), c.auth.UserID, c.tenant.TenantID, c.auth.SessionID, revokeReason)
	if err != nil {
		writeInternalError(w, err, "session revocation failed")
		return
	}
	revoked := 0
	for _, f := range families {
		if f.LiveRowID == "" {
			continue
		}
		revoked++
		h.recordSessionRevoked(r, c, f.ID, f.LiveRowID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
}

func (h *Handler) recordSessionRevoked(r *http.Request, c caller, familyID, liveRowID string) {
	metadata, _ := json.Marshal(map[string]any{"performed_by": c.auth.UserID, "family_id": familyID})
	row := authaudit.Row{
		EventType: "session.revoked",
		TenantID:  c.tenant.TenantID,
		UserID:    c.auth.UserID,
		SessionID: liveRowID,
		IPAddress: loginsession.ClientIP(r),
		UserAgent: r.UserAgent(),
		Success:   true,
		Metadata:  metadata,
	}
	if err := h.audit.Insert(r.Context(), row); err != nil {
		log.Warn().Err(err).Str("tenant", c.tenant.Slug).Msg("authsessions: session.revoked audit insert failed")
	}
}
