// Package authmepassword implements POST /auth/me/change-password —
// auth-internals.md §3 "Password change": a signed-in user replacing their
// own password. Same tenant/auth resolution pattern as authme.Handler.
//
// Out of scope: the global Argon2 concurrency limit (goerp#1032).
package authmepassword

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strconv"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const maxBodyBytes = 64 * 1024

// revokeReason is the sessions.revoke_reason value auth-internals.md §4
// defines for a password change.
const revokeReason = "password_change"

type Mailer interface {
	SendPasswordChanged(ctx context.Context, email string) error
}

// AuditRecorder is satisfied by authaudit.Store.
type AuditRecorder interface {
	Insert(ctx context.Context, row authaudit.Row) error
}

type Handler struct {
	tenants  *tenantresolve.Resolver
	auth     *authcheck.Checker
	users    *user.Store
	policies *password.PolicyStore
	sessions *sessionrevoke.Revoker
	mailer   Mailer
	audit    AuditRecorder
	hasher   *password.Hasher
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, users *user.Store, policies *password.PolicyStore, sessions *sessionrevoke.Revoker, mailer Mailer, audit AuditRecorder, hasher *password.Hasher) *Handler {
	return &Handler{tenants: tenants, auth: auth, users: users, policies: policies, sessions: sessions, mailer: mailer, audit: audit, hasher: hasher}
}

type changeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

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

func writeInternal(w http.ResponseWriter) {
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "password change failed")
}

func writeOverloaded(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(password.OverloadRetryAfterSeconds))
	writeJSONError(w, http.StatusServiceUnavailable, "overloaded", "too many password changes in progress, retry shortly")
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
			writeInternal(w)
		}
		return
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	// An API key has no session to keep and no user password to confirm.
	if err != nil || !authCtx.IsAuthenticated || authCtx.AuthMethod != "jwt" {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req changeRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	u, err := h.users.GetByID(ctx, authCtx.UserID)
	if err != nil {
		writeInternal(w)
		return
	}
	// A wrong current password doesn't touch the login lockout counter:
	// the caller already holds a session, and a lockout here would stop
	// them signing in again.
	if u.PasswordHash == nil {
		writeInvalidPassword(w)
		return
	}
	// Looked up before taking a slot, so the slot covers only hashing.
	policy, policyVersion, err := h.policies.Effective(ctx, tenantCtx.TenantID)
	if err != nil {
		writeInternal(w)
		return
	}
	slot, err := h.hasher.Acquire(ctx)
	if err != nil {
		writeOverloaded(w)
		return
	}
	defer slot.Release()
	match, _, err := slot.Verify(req.CurrentPassword, *u.PasswordHash)
	if err != nil {
		writeInternal(w)
		return
	}
	if !match {
		writeInvalidPassword(w)
		return
	}

	if err := policy.Validate(req.NewPassword, u.Email); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "auth.password_too_weak", err.Error())
		return
	}

	hash, err := slot.Hash(req.NewPassword)
	slot.Release()
	if err != nil {
		writeInternal(w)
		return
	}
	// Revoked before the write, so a revocation failure aborts with the
	// password unchanged (other devices may already be signed out, which
	// is what the user asked for anyway), and again after it to catch a
	// login with the old password in between.
	if err := h.sessions.RevokeOthersForUser(ctx, u.ID, authCtx.SessionID, revokeReason); err != nil {
		writeInternal(w)
		return
	}
	if err := h.users.SetPassword(ctx, u.ID, hash, tenantCtx.TenantID, policyVersion); err != nil {
		writeInternal(w)
		return
	}
	if err := h.sessions.RevokeOthersForUser(ctx, u.ID, authCtx.SessionID, revokeReason); err != nil {
		log.Error().Err(err).Str("user_id", u.ID).Msg("authmepassword: post-change session revocation failed")
	}

	h.recordAudit(ctx, authaudit.Row{
		EventType: "password.changed",
		TenantID:  tenantCtx.TenantID,
		UserID:    u.ID,
		SessionID: authCtx.SessionID,
		IPAddress: loginsession.ClientIP(r),
		UserAgent: r.UserAgent(),
		Success:   true,
	})
	h.notify(ctx, u.Email)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"status": "ok"})
}

func writeInvalidPassword(w http.ResponseWriter) {
	writeJSONError(w, http.StatusUnauthorized, "invalid_password", "current password is incorrect")
}

func (h *Handler) recordAudit(ctx context.Context, row authaudit.Row) {
	if h.audit == nil {
		log.Warn().Str("event", row.EventType).Msg("authmepassword: no audit recorder wired, event not recorded")
		return
	}
	if err := h.audit.Insert(ctx, row); err != nil {
		log.Warn().Err(err).Str("event", row.EventType).Msg("authmepassword: audit insert failed")
	}
}

func (h *Handler) notify(ctx context.Context, email string) {
	if h.mailer == nil {
		log.Warn().Msg("authmepassword: no mailer wired, password changed email not sent")
		return
	}
	if err := h.mailer.SendPasswordChanged(ctx, email); err != nil {
		log.Warn().Err(err).Msg("authmepassword: password changed email failed")
	}
}
