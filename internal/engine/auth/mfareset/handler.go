// Package mfareset implements POST /admin/users/{id}/mfa/reset —
// auth-internals.md §8 "Account recovery when all factors are lost": a
// tenant admin resetting a target user's MFA enrollment and sessions when
// that user has lost every enrolled factor and has no self-service path
// back in.
//
// Despite the "/admin/" path prefix, this is a tenant-facing route, not
// the operator-only internal/engine/adminapi surface (which runs on a
// separate loopback listener authenticated by the platform static
// token). auth-internals.md itself uses "/admin/" for both meanings —
// compare "POST /admin/tenants (operator)" against "POST
// /admin/users/{id}/suspend (tenant-admin action, requires admin role in
// this tenant)" a few lines later in the same doc. This route is the
// latter: Class A per §9's route classes table (Host-header tenant
// resolution, JWT/session authentication, admin-role authorization), the
// same shape goerp#307's /auth/mfa/reverify already established — calling
// tenantresolve.Resolver.ResolveByHost and authcheck.Checker.Authenticate
// directly since the generic middleware chain (goerp#91) that would
// normally run them doesn't exist yet.
package mfareset

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"

	"github.com/alexedwards/argon2id"
	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

// maxBodyBytes bounds the request body before JSON parsing — no shared
// config field or middleware covers builtin routes yet, same reasoning
// loginflow/mfaverify/mfareverify's own caps use.
const maxBodyBytes = 64 * 1024

// adminRoleName is the built-in role auth-internals.md §8's own wording
// ("requires the caller's own admin role") checks for — no shared
// constant exists for it anywhere in this codebase yet; role.Store's own
// SeedBuiltinRoles and every fixture that grants it use this same literal.
const adminRoleName = "admin"

// Mailer is the minimal interface this package needs — satisfied by
// mailer.SMTPMailer.SendMFAReset — defined here rather than imported from
// internal/engine/mailer, matching internal/engine/invite's own
// accept-an-interface-where-used convention.
type Mailer interface {
	SendMFAReset(ctx context.Context, email string) error
}

// AuditEmitter is the minimal interface this package needs for the
// mfa.admin_reset event. No concrete implementation exists yet — the
// real auth_audit_log table (auth-internals.md §17) isn't built
// (nexus-docs backlog #298, unfiled) — so a nil AuditEmitter is expected
// and handled the same way internal/engine/invite.Store's own optional
// AuditEmitter is: logged as a warning rather than silently dropped or
// failing the request.
type AuditEmitter interface {
	Emit(ctx context.Context, tenantSlug, eventName string, payload map[string]any) error
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
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, users *user.Store, roles *role.Store, mfaStore *mfa.Store, sessions *sessionrevoke.Revoker, mailer Mailer, audit AuditEmitter) *Handler {
	return &Handler{
		tenants:  tenants,
		auth:     auth,
		users:    users,
		roles:    roles,
		mfa:      mfaStore,
		sessions: sessions,
		mailer:   mailer,
		audit:    audit,
	}
}

type resetRequest struct {
	// Password is the caller's own current password — auth-internals.md
	// §8's "current-password confirmation" requirement, the same bar
	// issuing an API key uses (§20). Never the target user's password.
	Password string `json:"password"`
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

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Step 5 (Class A): Host-header tenant resolution.
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
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "reset failed")
		}
		return
	}

	// Step 7 (Class A, JWT branch): requires a currently-valid access
	// token — this is a real tenant-session user, never the platform
	// static admin token.
	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return
	}

	if !slices.Contains(authCtx.RolesLive, adminRoleName) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "admin role required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req resetRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	if !h.confirmCallerPassword(ctx, authCtx.UserID, req.Password) {
		writeJSONError(w, http.StatusUnauthorized, "invalid_password", "current password confirmation failed")
		return
	}

	targetID := route.ParamsFromContext(ctx)["id"]
	if targetID == "" {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return
	}

	// A target user id is a global users.id, not scoped to this tenant —
	// membership must be checked explicitly before touching anything,
	// or a tenant admin could reset MFA/sessions for a user who isn't
	// even a member here.
	isMember, err := h.roles.IsMember(ctx, tenantCtx.Slug, targetID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "reset failed")
		return
	}
	if !isMember {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return
	}

	target, err := h.users.GetByID(ctx, targetID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "reset failed")
		return
	}

	// user_mfa has no tenant scoping at all (a factor belongs to the
	// user globally) — this genuinely revokes every enrolled factor
	// regardless of which tenant it was enrolled through, matching the
	// doc's own "Revoke every enrolled user_mfa row for {id}" wording.
	if err := h.mfa.RevokeAllForUser(ctx, targetID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "reset failed")
		return
	}
	// Sessions, unlike user_mfa, are tenant-scoped — only this tenant's
	// sessions for the target are revoked, per the doc's own "all their
	// active sessions in the tenant" wording.
	if err := h.sessions.RevokeAllForUserInTenant(ctx, targetID, tenantCtx.TenantID, "admin_mfa_reset"); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "reset failed")
		return
	}

	h.emitAudit(ctx, tenantCtx.Slug, authCtx.UserID, targetID)
	h.notify(ctx, target.Email)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"status": "ok"})
}

// confirmCallerPassword re-verifies the caller's own current password —
// never the target's. A nil PasswordHash (shouldn't happen for a caller
// who just authenticated via a real access token, since password login
// requires one) is treated as a failed confirmation, not a system error.
func (h *Handler) confirmCallerPassword(ctx context.Context, callerID, password string) bool {
	caller, err := h.users.GetByID(ctx, callerID)
	if err != nil || caller.PasswordHash == nil {
		return false
	}
	match, err := argon2id.ComparePasswordAndHash(password, *caller.PasswordHash)
	return err == nil && match
}

func (h *Handler) emitAudit(ctx context.Context, tenantSlug, performedBy, targetUserID string) {
	if h.audit == nil {
		log.Warn().Str("tenant", tenantSlug).Str("event", "mfa.admin_reset").Msg("mfareset: no audit emitter wired, event not recorded")
		return
	}
	if err := h.audit.Emit(ctx, tenantSlug, "mfa.admin_reset", map[string]any{
		"performed_by":   performedBy,
		"target_user_id": targetUserID,
	}); err != nil {
		log.Warn().Err(err).Str("tenant", tenantSlug).Str("event", "mfa.admin_reset").Msg("mfareset: audit emit failed")
	}
}

func (h *Handler) notify(ctx context.Context, email string) {
	if h.mailer == nil {
		log.Warn().Str("email", email).Msg("mfareset: no mailer wired, notification email not sent")
		return
	}
	if err := h.mailer.SendMFAReset(ctx, email); err != nil {
		log.Warn().Err(err).Str("email", email).Msg("mfareset: notification email failed")
	}
}
