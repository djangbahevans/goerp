// Package mfareset lets tenant administrators reset member MFA and sessions in their own tenant.
package mfareset

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const maxBodyBytes = 64 * 1024

const adminRoleName = "admin"

type Mailer interface {
	SendMFAReset(ctx context.Context, email, tenantName string) error
}

type AuditEmitter interface {
	Emit(ctx context.Context, tenantSlug, eventName, userID, actorUserID string, payload map[string]any) error
}

type Handler struct {
	tenants  *tenantresolve.Resolver
	auth     *authcheck.Checker
	users    *user.Store
	roles    *role.Store
	mfa      *mfa.Store
	sessions *sessionrevoke.Revoker
	mailer   Mailer
	audit    AuditEmitter
	hasher   *password.Hasher
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, users *user.Store, roles *role.Store, mfaStore *mfa.Store, sessions *sessionrevoke.Revoker, mailer Mailer, audit AuditEmitter, hasher *password.Hasher) *Handler {
	return &Handler{
		tenants:  tenants,
		auth:     auth,
		users:    users,
		roles:    roles,
		mfa:      mfaStore,
		sessions: sessions,
		mailer:   mailer,
		audit:    audit,
		hasher:   hasher,
	}
}

type resetRequest struct {
	// Password is the caller's own current password — auth-internals.md
	// §8's "current-password confirmation" requirement, the same bar
	// issuing an API key uses (§20). Never the target user's password.
	Password string `json:"password"`
}

func writeJSON(w http.ResponseWriter, v any) {
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantCtx, err := h.tenants.ResolveByHost(ctx, r.Host)
	if err != nil {
		switch {
		case errors.Is(err, tenantresolve.ErrTenantNotFound):
			httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "not found")
		case errors.Is(err, tenantresolve.ErrTenantSuspended):
			httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
		case errors.Is(err, tenantresolve.ErrTenantOffboarding):
			httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
		default:
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reset failed")
		}

		return
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return
	}

	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if authcheck.WritePasswordChangeRequired(r.Context(), w, err) {
		return
	}

	if err != nil || !authCtx.IsAuthenticated {
		httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return
	}

	if !slices.Contains(authCtx.RolesLive, adminRoleName) {
		httperr.Write(r.Context(), w, http.StatusForbidden, "forbidden", "admin role required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req resetRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	confirmed, err := h.confirmCallerPassword(ctx, authCtx.UserID, req.Password)
	if err != nil {
		w.Header().Set("Retry-After", strconv.Itoa(password.OverloadRetryAfterSeconds))
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "overloaded", "too many password checks in progress, retry shortly")
		return
	}

	if !confirmed {
		httperr.Write(r.Context(), w, http.StatusUnauthorized, "invalid_password", "current password confirmation failed")
		return
	}

	targetID := route.ParamsFromContext(ctx)["id"]
	if targetID == "" {
		httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "not found")
		return
	}

	// A target user id is a global users.id, not scoped to this tenant —
	// membership must be checked explicitly before touching anything,
	// or a tenant admin could reset MFA/sessions for a user who isn't
	// even a member here.
	isMember, err := h.roles.IsMember(ctx, tenantCtx.Slug, targetID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reset failed")
		return
	}

	if !isMember {
		httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "not found")
		return
	}

	target, err := h.users.GetByID(ctx, targetID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reset failed")
		return
	}

	var revokedIDs []string
	err = h.mfa.WithTx(ctx, func(tx *sql.Tx) error {
		if err := h.mfa.LockUserTx(ctx, tx, targetID); err != nil {
			return err
		}

		if err := h.mfa.RevokeTenantTx(ctx, tx, targetID, tenantCtx.TenantID); err != nil {
			return err
		}

		if err := h.mfa.SetResetTx(ctx, tx, targetID, tenantCtx.TenantID, true); err != nil {
			return err
		}

		var err error
		revokedIDs, err = h.sessions.RevokeAllForUserInTenantTx(ctx, tx, targetID, tenantCtx.TenantID, "admin_mfa_reset")
		return err
	})
	if err != nil {
		httperr.Write(ctx, w, http.StatusInternalServerError, "internal_error", "reset failed")
		return
	}

	if err := h.sessions.Blocklist(ctx, revokedIDs); err != nil {
		log.Error().Err(err).Msg("mfareset: blocklist sessions after tenant reset failed")
	}

	h.emitAudit(ctx, tenantCtx.Slug, authCtx.UserID, targetID)
	h.notify(ctx, target.Email, tenantCtx.Name)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"status": "ok"})
}

// Accounts created through federated sign-in can have no password hash; confirmation then fails.
func (h *Handler) confirmCallerPassword(ctx context.Context, callerID, plain string) (bool, error) {
	caller, err := h.users.GetByID(ctx, callerID)
	if err != nil || caller.PasswordHash == nil {
		return false, nil
	}

	slot, err := h.hasher.Acquire(ctx)
	if err != nil {
		return false, err
	}

	defer slot.Release()
	match, _, err := slot.Verify(plain, *caller.PasswordHash)
	return err == nil && match, nil
}

func (h *Handler) emitAudit(ctx context.Context, tenantSlug, performedBy, targetUserID string) {
	if h.audit == nil {
		log.Warn().Str("tenant", tenantSlug).Str("event", "mfa.admin_reset").Msg("mfareset: no audit emitter wired, event not recorded")
		return
	}

	if err := h.audit.Emit(ctx, tenantSlug, "mfa.admin_reset", targetUserID, performedBy, nil); err != nil {
		log.Warn().Err(err).Str("tenant", tenantSlug).Str("event", "mfa.admin_reset").Msg("mfareset: audit emit failed")
	}
}

func (h *Handler) notify(ctx context.Context, email, tenantName string) {
	if h.mailer == nil {
		log.Warn().Str("email", email).Msg("mfareset: no mailer wired, notification email not sent")
		return
	}

	if err := h.mailer.SendMFAReset(ctx, email, tenantName); err != nil {
		log.Warn().Err(err).Str("email", email).Msg("mfareset: notification email failed")
	}
}
