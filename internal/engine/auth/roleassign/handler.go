// Package roleassign implements the admin flow to grant and revoke a
// tenant member's roles (goerp#619): POST /admin/users/{id}/roles grants,
// DELETE /admin/users/{id}/roles/{role} revokes. Both invalidate
// permcache.RoleCache and every active session's stale-roles marker
// (auth-internals.md §14 "Cache invalidation on role change" steps 1 and
// 3 — step 2, ABAC policy-cache invalidation, doesn't apply yet: no ABAC
// evaluation/policy-cache system exists anywhere in the engine), then
// broadcast role.changed on ws.UserChannel (goerp#616's ws.Hub) so an
// already-open session's PermissionContext can refresh without reload.
//
// Despite the "/admin/" path prefix, mirrors internal/engine/auth/mfareset's
// own doc comment: this is Class A tenant-facing (Host-header tenant
// resolution, JWT/session authentication, admin-role authorization), not
// the operator-only internal/engine/adminapi surface.
package roleassign

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/ws"
)

// maxBodyBytes bounds the request body before JSON parsing — same cap
// mfareset/loginflow/mfaverify/mfareverify's own builtin routes use, since
// no shared config field or middleware covers builtin routes yet.
const maxBodyBytes = 64 * 1024

// adminRoleName mirrors mfareset's own literal — no shared constant for
// it exists anywhere in this codebase yet.
const adminRoleName = "admin"

// AuditEmitter mirrors mfareset's own minimal, accept-an-interface-where-used
// convention. No concrete implementation exists yet (nexus-docs backlog
// #298, unfiled) — a nil AuditEmitter is expected and logged as a warning
// rather than failing the request.
type AuditEmitter interface {
	Emit(ctx context.Context, tenantSlug, eventName string, payload map[string]any) error
}

type Handler struct {
	tenants   *tenantresolve.Resolver
	auth      *authcheck.Checker
	roles     *role.Store
	roleCache *permcache.RoleCache
	sessions  *sessionrevoke.Revoker
	hub       *ws.Hub
	audit     AuditEmitter
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, roles *role.Store, roleCache *permcache.RoleCache, sessions *sessionrevoke.Revoker, hub *ws.Hub, audit AuditEmitter) *Handler {
	return &Handler{
		tenants:   tenants,
		auth:      auth,
		roles:     roles,
		roleCache: roleCache,
		sessions:  sessions,
		hub:       hub,
		audit:     audit,
	}
}

type assignRequest struct {
	Role string `json:"role"`
}

// writeJSON matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own — same as mfareset's own
// writeJSON.
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

// resolved is what ServeAssign and ServeRevoke both need before touching
// anything — tenant/auth resolution, admin-role check, and target
// tenant-membership check are identical for grant and revoke.
type resolved struct {
	tenantCtx *tenantresolve.TenantContext
	authCtx   *authcheck.AuthContext
	targetID  string
}

func (h *Handler) resolve(w http.ResponseWriter, r *http.Request) (resolved, bool) {
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
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		}
		return resolved{}, false
	}

	// Step 7 (Class A, JWT branch): requires a currently-valid access token.
	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return resolved{}, false
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return resolved{}, false
	}

	if !slices.Contains(authCtx.RolesLive, adminRoleName) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "admin role required")
		return resolved{}, false
	}

	targetID := route.ParamsFromContext(ctx)["id"]
	if targetID == "" {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return resolved{}, false
	}

	// A target user id is a global users.id, not scoped to this tenant —
	// membership must be checked explicitly before touching anything, or a
	// tenant admin could grant/revoke roles for a user who isn't even a
	// member here (mirrors mfareset's own identical check).
	isMember, err := h.roles.IsMember(ctx, tenantCtx.Slug, targetID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		return resolved{}, false
	}
	if !isMember {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return resolved{}, false
	}

	return resolved{tenantCtx: tenantCtx, authCtx: authCtx, targetID: targetID}, true
}

// ServeAssign is POST /admin/users/{id}/roles.
func (h *Handler) ServeAssign(w http.ResponseWriter, r *http.Request) {
	req, ok := h.resolve(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body assignRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil || body.Role == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	roleID, err := h.roles.GetRoleByName(ctx, req.tenantCtx.Slug, body.Role)
	if err != nil {
		if errors.Is(err, role.ErrRoleNotFound) {
			writeJSONError(w, http.StatusNotFound, "role_not_found", "unknown role")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		return
	}

	if err := h.roles.AssignRole(ctx, req.tenantCtx.Slug, req.targetID, roleID, req.authCtx.UserID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		return
	}

	h.invalidateAndBroadcast(ctx, req.tenantCtx, req.targetID, body.Role, "assigned")
	h.emitAudit(ctx, req.tenantCtx.Slug, "role.assigned", req.authCtx.UserID, req.targetID, body.Role)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"status": "ok"})
}

// ServeRevoke is DELETE /admin/users/{id}/roles/{role}.
func (h *Handler) ServeRevoke(w http.ResponseWriter, r *http.Request) {
	req, ok := h.resolve(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	roleName := route.ParamsFromContext(ctx)["role"]
	if roleName == "" {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return
	}

	roleID, err := h.roles.GetRoleByName(ctx, req.tenantCtx.Slug, roleName)
	if err != nil {
		if errors.Is(err, role.ErrRoleNotFound) {
			writeJSONError(w, http.StatusNotFound, "role_not_found", "unknown role")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		return
	}

	if err := h.roles.RevokeRole(ctx, req.tenantCtx.Slug, req.targetID, roleID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		return
	}

	h.invalidateAndBroadcast(ctx, req.tenantCtx, req.targetID, roleName, "revoked")
	h.emitAudit(ctx, req.tenantCtx.Slug, "role.revoked", req.authCtx.UserID, req.targetID, roleName)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"status": "ok"})
}

// invalidateAndBroadcast is ServeAssign/ServeRevoke's shared tail —
// auth-internals.md §14's steps 1 and 3 (step 2, ABAC policy-cache
// invalidation, doesn't apply: see this package's own doc comment), then
// the live-session broadcast. Best-effort throughout: the mutation itself
// already committed by the time this runs, and a cache/broadcast failure
// here degrades to "reflected after the cache's normal TTL" or "reflected
// on next page load" rather than losing the grant/revoke itself — not
// worth failing a request whose actual mutation already succeeded.
func (h *Handler) invalidateAndBroadcast(ctx context.Context, tenantCtx *tenantresolve.TenantContext, userID, roleName, action string) {
	if err := h.roleCache.Invalidate(ctx, tenantCtx.TenantID, userID); err != nil {
		log.Warn().Err(err).Str("tenant", tenantCtx.Slug).Str("user", userID).Msg("roleassign: role cache invalidation failed")
	}
	if err := h.sessions.MarkRolesStaleForUserInTenant(ctx, userID, tenantCtx.TenantID); err != nil {
		log.Warn().Err(err).Str("tenant", tenantCtx.Slug).Str("user", userID).Msg("roleassign: mark roles stale failed")
	}

	if h.hub == nil {
		return
	}
	payload := map[string]string{"role": roleName, "action": action}
	if _, err := h.hub.Broadcast(ctx, ws.UserChannel(userID), "role.changed", payload); err != nil {
		log.Warn().Err(err).Str("tenant", tenantCtx.Slug).Str("user", userID).Msg("roleassign: broadcast failed")
	}
}

func (h *Handler) emitAudit(ctx context.Context, tenantSlug, eventName, performedBy, targetUserID, roleName string) {
	if h.audit == nil {
		log.Warn().Str("tenant", tenantSlug).Str("event", eventName).Msg("roleassign: no audit emitter wired, event not recorded")
		return
	}
	if err := h.audit.Emit(ctx, tenantSlug, eventName, map[string]any{
		"performed_by":   performedBy,
		"target_user_id": targetUserID,
		"role":           roleName,
	}); err != nil {
		log.Warn().Err(err).Str("tenant", tenantSlug).Str("event", eventName).Msg("roleassign: audit emit failed")
	}
}
