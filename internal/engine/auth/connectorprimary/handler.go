// Package connectorprimary implements PATCH /admin/connectors/{name}/set-primary for a
// tenant admin selecting the active connector provider. The route uses tenant session
// authentication despite its /admin/ prefix.
package connectorprimary

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
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

const adminRoleName = "admin"

type AuditEmitter interface {
	Emit(ctx context.Context, tenantSlug, eventName, userID, actorUserID string, payload map[string]any) error
}

type Handler struct {
	tenants   *tenantresolve.Resolver
	auth      *authcheck.Checker
	selection *providerselect.Store
	audit     AuditEmitter
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, selection *providerselect.Store, audit AuditEmitter) *Handler {
	return &Handler{tenants: tenants, auth: auth, selection: selection, audit: audit}
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

func (h *Handler) ServeSetPrimary(w http.ResponseWriter, r *http.Request) {
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
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		}
		return
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if authcheck.WritePasswordChangeRequired(r.Context(), w, err) {
		return
	}
	if err != nil || !authCtx.IsAuthenticated {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return
	}
	if !slices.Contains(authCtx.RolesLive, adminRoleName) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "admin role required")
		return
	}

	moduleName := route.ParamsFromContext(ctx)["name"]
	if moduleName == "" {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return
	}

	var input struct {
		Category string `json:"category"`
	}
	if err := json.UnmarshalRead(http.MaxBytesReader(w, r.Body, 4096), &input, json.RejectUnknownMembers(true)); err != nil || input.Category == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "a provider category is required")
		return
	}
	category := input.Category
	if !providerselect.IsCategory(category) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_provider_category", "unrecognized provider category")
		return
	}

	err = h.selection.SetPrimary(ctx, tenantCtx.TenantID, moduleName, category, authCtx.UserID)
	if err != nil {
		switch {
		case errors.Is(err, providerselect.ErrModuleNotEnabled):
			writeJSONError(w, http.StatusNotFound, "not_found", "connector is not installed and enabled")
		case errors.Is(err, providerselect.ErrModuleNotProvider):
			writeJSONError(w, http.StatusUnprocessableEntity, "not_a_provider", "connector does not provide a provider category")
		case errors.Is(err, providerselect.ErrNotSingleActive):
			writeJSONError(w, http.StatusUnprocessableEntity, "multi_active_category", "this connector's category has no primary provider")
		default:
			log.Error().Err(err).Str("tenant", tenantCtx.Slug).Str("module", moduleName).Msg("connectorprimary: set primary failed")
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
		}
		return
	}

	h.emitAudit(ctx, tenantCtx.Slug, authCtx.UserID, moduleName, category)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"module_name": moduleName, "category": category})
}

func (h *Handler) emitAudit(ctx context.Context, tenantSlug, performedBy, moduleName, category string) {
	const event = "connector.primary_changed"
	if h.audit == nil {
		log.Warn().Str("tenant", tenantSlug).Str("event", event).Msg("connectorprimary: no audit emitter wired, event not recorded")
		return
	}
	if err := h.audit.Emit(ctx, tenantSlug, event, "", performedBy, map[string]any{
		"module_name": moduleName,
		"category":    category,
	}); err != nil {
		log.Warn().Err(err).Str("tenant", tenantSlug).Str("event", event).Msg("connectorprimary: audit emit failed")
	}
}
