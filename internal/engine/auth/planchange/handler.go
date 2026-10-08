// Package planchange implements POST /admin/tenant/plan for tenant admins and broadcasts
// plan.changed after invalidating cached entitlements. The route uses tenant session
// authentication despite its /admin/ prefix.
package planchange

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
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
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
// accept-an-interface-where-used convention, satisfied by authaudit.Store.
// A nil AuditEmitter is logged as a warning rather than failing the request.
type AuditEmitter interface {
	Emit(ctx context.Context, tenantSlug, eventName, userID, actorUserID string, payload map[string]any) error
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

// ServeHTTP is POST /admin/tenant/plan.
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
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "request failed")
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
	var body changePlanRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil || body.Plan == "" {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	requestedPlan := tenant.Plan(body.Plan)
	if !slices.Contains(tenant.AllPlans, requestedPlan) {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_plan", "unknown plan")
		return
	}

	plan, err := h.billing.GetPlanByName(ctx, body.Plan)
	if err != nil {
		if errors.Is(err, billing.ErrPlanNotFound) {
			httperr.Write(r.Context(), w, http.StatusNotFound, "plan_not_found", "unknown plan")
			return
		}
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "request failed")
		return
	}

	if _, err := h.billing.ChangeTenantPlan(ctx, tenantCtx.TenantID, plan.ID); err != nil {
		switch {
		case errors.Is(err, billing.ErrNoActiveSubscription):
			httperr.Write(r.Context(), w, http.StatusConflict, "no_active_subscription", "tenant has no active subscription")
		case errors.Is(err, billing.ErrMultipleActiveSubscriptions):
			log.Error().Err(err).Str("tenant", tenantCtx.Slug).Msg("planchange: tenant has more than one active subscription, moved all of them")
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "request failed")
		default:
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "request failed")
		}
		return
	}

	// Subscription and tenant metadata commit in separate stores, leaving a temporary
	// mismatch until UpdatePlan succeeds.
	if _, err := h.tenantStore.UpdatePlan(ctx, tenantCtx.Slug, requestedPlan); err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "request failed")
		return
	}

	// A stale domain-cache entry exposes the old plan until its TTL expires, so
	// invalidation failure is reported.
	if err := h.invalidateDomainCache(ctx, tenantCtx.TenantID); err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "plan changed, but invalidating the domain cache failed")
		return
	}

	h.invalidateAndBroadcast(ctx, tenantCtx, body.Plan)
	h.emitAudit(ctx, tenantCtx.Slug, authCtx.UserID, body.Plan)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"status": "ok"})
}

// invalidateDomainCache is needed because tenantByDomain's cached entry
// is a full tenant.Tenant, Plan included: without it ResolveByHost keeps
// returning the old plan for up to domainCacheTTL.
func (h *Handler) invalidateDomainCache(ctx context.Context, tenantID string) error {
	return tenantresolve.InvalidateTenantDomains(ctx, h.tenantStore, h.cache, tenantID)
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
	if err := h.audit.Emit(ctx, tenantSlug, "plan.changed", "", performedBy, map[string]any{
		"plan": planName,
	}); err != nil {
		log.Warn().Err(err).Str("tenant", tenantSlug).Str("event", "plan.changed").Msg("planchange: audit emit failed")
	}
}
