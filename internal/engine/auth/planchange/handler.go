// Package planchange implements POST /admin/tenant/plan (goerp#620/#628):
// a tenant admin moving their own tenant's active subscription onto a
// different plan, then invalidating the cached EntitlementSet and
// broadcasting plan.changed on ws.TenantChannel (goerp#621's ws.Hub) so an
// already-open session's PermissionContext can refresh without reload.
//
// Despite the "/admin/" path prefix, mirrors internal/engine/auth/mfareset's
// own doc comment: this is Class A tenant-facing (Host-header tenant
// resolution, JWT/session authentication, admin-role authorization), not
// the operator-only internal/engine/adminapi surface.
package planchange

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/ws"
)

// maxBodyBytes bounds the request body before JSON parsing — same cap
// mfareset/roleassign's own builtin routes use, since no shared config
// field or middleware covers builtin routes yet.
const maxBodyBytes = 64 * 1024

// adminRoleName mirrors mfareset/roleassign's own literal — no shared
// constant for it exists anywhere in this codebase yet.
const adminRoleName = "admin"

// AuditEmitter mirrors mfareset/roleassign's own minimal,
// accept-an-interface-where-used convention. No concrete implementation
// exists yet (nexus-docs backlog #298, unfiled) — a nil AuditEmitter is
// expected and logged as a warning rather than failing the request.
type AuditEmitter interface {
	Emit(ctx context.Context, tenantSlug, eventName string, payload map[string]any) error
}

type Handler struct {
	tenants     *tenantresolve.Resolver
	auth        *authcheck.Checker
	billing     *billing.Store
	tenantStore *tenant.Store
	cache       *cache.Client
	hub         *ws.Hub
	audit       AuditEmitter
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, billingStore *billing.Store, tenantStore *tenant.Store, cacheClient *cache.Client, hub *ws.Hub, audit AuditEmitter) *Handler {
	return &Handler{
		tenants:     tenants,
		auth:        auth,
		billing:     billingStore,
		tenantStore: tenantStore,
		cache:       cacheClient,
		hub:         hub,
		audit:       audit,
	}
}

type changePlanRequest struct {
	Plan string `json:"plan"`
}

// writeJSON matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own — same as mfareset/
// roleassign's own writeJSON.
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

// ServeHTTP is POST /admin/tenant/plan.
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
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		}
		return
	}

	// Step 7 (Class A, JWT branch): requires a currently-valid access token.
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
	var body changePlanRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil || body.Plan == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	requestedPlan := tenant.Plan(body.Plan)
	if !slices.Contains(tenant.AllPlans, requestedPlan) {
		writeJSONError(w, http.StatusBadRequest, "invalid_plan", "unknown plan")
		return
	}

	plan, err := h.billing.GetPlanByName(ctx, body.Plan)
	if err != nil {
		if errors.Is(err, billing.ErrPlanNotFound) {
			writeJSONError(w, http.StatusNotFound, "plan_not_found", "unknown plan")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		return
	}

	if _, err := h.billing.ChangeTenantPlan(ctx, tenantCtx.TenantID, plan.ID); err != nil {
		switch {
		case errors.Is(err, billing.ErrNoActiveSubscription):
			writeJSONError(w, http.StatusConflict, "no_active_subscription", "tenant has no active subscription")
		case errors.Is(err, billing.ErrMultipleActiveSubscriptions):
			log.Error().Err(err).Str("tenant", tenantCtx.Slug).Msg("planchange: tenant has more than one active subscription, moved all of them")
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		default:
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		}
		return
	}

	// tenants.plan is now stale until UpdatePlan below commits, and the
	// domain cache (invalidated further down) is stale until that
	// invalidation runs — both windows are unavoidable without a shared
	// cross-store transaction, which no two stores in this codebase share
	// today; see this package's own tests for the reasoning this doesn't
	// attempt to close.
	if _, err := h.tenantStore.UpdatePlan(ctx, tenantCtx.Slug, requestedPlan); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		return
	}

	// Unlike the best-effort tail below, a stale domain-cache entry is
	// directly user-visible (GET /auth/me's Plan field, resolved from the
	// same cached row) for up to the cache's full TTL — so this mirrors
	// adminapi.tenantHandlers' own invalidateDomainCache convention
	// (suspend/unsuspend) and fails the request rather than silently
	// degrading.
	if err := h.invalidateDomainCache(ctx, tenantCtx.TenantID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "plan changed, but invalidating the domain cache failed")
		return
	}

	h.invalidateAndBroadcast(ctx, tenantCtx, body.Plan)
	h.emitAudit(ctx, tenantCtx.Slug, authCtx.UserID, body.Plan)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"status": "ok"})
}

// invalidateDomainCache deletes every tenantresolve domain-cache entry
// for tenantID, mirroring internal/engine/adminapi.tenantHandlers' own
// helper of the same name — tenantByDomain's cached entry is a full
// tenant.Tenant (including Plan), so a plan change must invalidate it the
// same way a status change already does, or ResolveByHost keeps
// returning the old plan for up to domainCacheTTL.
func (h *Handler) invalidateDomainCache(ctx context.Context, tenantID string) error {
	domains, err := h.tenantStore.DomainsForTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("list domains: %w", err)
	}
	for _, d := range domains {
		if err := h.cache.Delete(ctx, tenantresolve.DomainCacheKey(d.Domain)); err != nil {
			return fmt.Errorf("invalidate domain cache for %q: %w", d.Domain, err)
		}
	}
	return nil
}

// invalidateAndBroadcast is ServeHTTP's shared tail — best-effort
// throughout, same reasoning roleassign's own invalidateAndBroadcast
// documents: the mutation itself already committed by the time this
// runs, and a cache/broadcast failure here degrades to "reflected after
// the cache's normal TTL" or "reflected on next page load" rather than
// losing the plan change itself.
func (h *Handler) invalidateAndBroadcast(ctx context.Context, tenantCtx *tenantresolve.TenantContext, planName string) {
	if err := h.cache.Delete(ctx, tenantresolve.EntitlementCacheKey(tenantCtx.TenantID)); err != nil {
		log.Warn().Err(err).Str("tenant", tenantCtx.Slug).Msg("planchange: entitlement cache invalidation failed")
	}

	if h.hub == nil {
		return
	}
	payload := map[string]string{"plan": planName}
	if _, err := h.hub.Broadcast(ctx, ws.TenantChannel(tenantCtx.TenantID), "plan.changed", payload); err != nil {
		log.Warn().Err(err).Str("tenant", tenantCtx.Slug).Msg("planchange: broadcast failed")
	}
}

func (h *Handler) emitAudit(ctx context.Context, tenantSlug, performedBy, planName string) {
	if h.audit == nil {
		log.Warn().Str("tenant", tenantSlug).Str("event", "plan.changed").Msg("planchange: no audit emitter wired, event not recorded")
		return
	}
	if err := h.audit.Emit(ctx, tenantSlug, "plan.changed", map[string]any{
		"performed_by": performedBy,
		"plan":         planName,
	}); err != nil {
		log.Warn().Err(err).Str("tenant", tenantSlug).Str("event", "plan.changed").Msg("planchange: audit emit failed")
	}
}
