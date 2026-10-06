// Package adminconnectors implements the tenant-admin connector configuration
// API behind the shell's connector pages (shell-ux.md §5.4, connector-guide.md
// §6): GET /admin/connectors, GET /admin/connectors/{name}, PATCH
// /admin/config, POST /admin/connectors/{name}/config/{key}/rotate and DELETE
// /admin/connectors/{name}/webhook.
//
// Like connectorprimary, these are Class A tenant-facing routes despite the
// "/admin/" prefix: Host-header tenant resolution, session authentication and
// the admin role in the resolved tenant.
package adminconnectors

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
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

const adminRoleName = "admin"

// maskedValue is what a read returns in place of a set encrypted value, and
// what a write treats as "unchanged".
const maskedValue = "***"

// Registry exposes the current module snapshot.
type Registry interface {
	Snapshot() *registry.RegistrySnapshot
}

// ConfigStore reads and writes a tenant's module_config rows.
type ConfigStore interface {
	ModuleConfigRows(ctx context.Context, tenantSchema, moduleName string) (map[string]tenantconfig.ModuleConfigRow, error)
	SetModuleConfigMany(ctx context.Context, tenantID, tenantSchema, moduleName string, values []tenantconfig.ModuleConfigValue, updatedBy string) error
}

// ConfigCache drops a resolver's cached value for a key once it is rewritten.
type ConfigCache interface {
	Invalidate(tenantID, key string)
}

// Endpoints manages a connector's webhook endpoint token.
type Endpoints interface {
	ActiveEndpoint(ctx context.Context, tenantID, moduleName string) (string, error)
	MintEndpoint(ctx context.Context, tenantID, moduleName string) (string, error)
	RevokeEndpoint(ctx context.Context, tenantID, moduleName string) error
}

// Providers answers provider-category questions for a tenant.
type Providers interface {
	Resolve(ctx context.Context, tenantID, category string) (string, error)
	EnabledProviders(ctx context.Context, tenantID, category string) ([]string, error)
}

// TenantModules lists the modules a tenant has disabled.
type TenantModules interface {
	DisabledModulesForTenant(ctx context.Context, tenantID string) ([]string, error)
}

// AuditEmitter records an audit event; a nil emitter is logged, not fatal.
type AuditEmitter interface {
	Emit(ctx context.Context, tenantSlug, eventName, userID, actorUserID string, payload map[string]any) error
}

// Deps are the Handler's collaborators.
type Deps struct {
	Tenants   *tenantresolve.Resolver
	Auth      *authcheck.Checker
	Registry  Registry
	Config    ConfigStore
	Cache     ConfigCache
	Providers Providers
	Endpoints Endpoints
	Modules   TenantModules
	Keys      *rowcrypt.RowKeySet
	Audit     AuditEmitter
}

type Handler struct {
	Deps
}

func NewHandler(deps Deps) *Handler {
	return &Handler{Deps: deps}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeErrorDetails(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "details": details}})
}

// caller is the resolved tenant and authenticated admin of a request.
type caller struct {
	tenantID   string
	tenantSlug string
	userID     string
}

// authorize resolves the tenant from the Host header, authenticates the
// session and requires the admin role, answering the failure itself.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (caller, bool) {
	ctx := r.Context()

	tenantCtx, err := h.Tenants.ResolveByHost(ctx, r.Host)
	if err != nil {
		switch {
		case errors.Is(err, tenantresolve.ErrTenantNotFound):
			writeError(w, http.StatusNotFound, "not_found", "not found")
		case errors.Is(err, tenantresolve.ErrTenantSuspended):
			writeError(w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
		case errors.Is(err, tenantresolve.ErrTenantOffboarding):
			writeError(w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "request failed")
		}
		return caller{}, false
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return caller{}, false
	}
	authCtx, err := h.Auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if authcheck.WritePasswordChangeRequired(ctx, w, err) {
		return caller{}, false
	}
	if err != nil || !authCtx.IsAuthenticated {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return caller{}, false
	}
	if !slices.Contains(authCtx.RolesLive, adminRoleName) {
		writeError(w, http.StatusForbidden, "forbidden", "admin role required")
		return caller{}, false
	}
	return caller{tenantID: tenantCtx.TenantID, tenantSlug: tenantCtx.Slug, userID: authCtx.UserID}, true
}

func internalError(w http.ResponseWriter, c caller, operation string, err error) {
	log.Error().Err(err).Str("tenant", c.tenantSlug).Msg("adminconnectors: " + operation)
	writeError(w, http.StatusInternalServerError, "internal_error", "request failed")
}

func (h *Handler) emitAudit(ctx context.Context, c caller, event string, payload map[string]any) {
	if h.Audit == nil {
		log.Warn().Str("tenant", c.tenantSlug).Str("event", event).Msg("adminconnectors: no audit emitter wired, event not recorded")
		return
	}
	if err := h.Audit.Emit(ctx, c.tenantSlug, event, "", c.userID, payload); err != nil {
		log.Warn().Err(err).Str("tenant", c.tenantSlug).Str("event", event).Msg("adminconnectors: audit emit failed")
	}
}
