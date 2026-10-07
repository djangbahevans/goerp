// Package adminmodules implements the tenant admin module endpoints
// (shell-ux.md §5.3, multitenancy-internals.md §8): GET /admin/modules
// lists the installed modules with their entitlement and enabled state,
// and PATCH /admin/modules/{name}/settings lets an admin disable a module
// the plan entitles, or enable it again.
//
// Like adminroles and planchange, these are Class A tenant-facing routes
// despite the "/admin/" prefix: Host-header tenant resolution, session
// authentication and the admin role in the resolved tenant.
package adminmodules

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/ws"
)

const (
	adminRoleName = "admin"
	maxBodyBytes  = 64 * 1024

	// modulesChangedEvent tells a tenant's open sessions to refetch their
	// permissions, which carry the enabled module list.
	modulesChangedEvent = "modules.changed"
)

// Registry is satisfied by registry.ModuleRegistry.
type Registry interface {
	Snapshot() *registry.RegistrySnapshot
}

// Settings is satisfied by billing.Store.
type Settings interface {
	SetModuleEnabledForTenant(ctx context.Context, tenantID, moduleName string, enabled bool, disabledBy *string) error
}

// AuditEmitter records an audit event; a nil emitter is logged, not fatal.
type AuditEmitter interface {
	Emit(ctx context.Context, tenantSlug, eventName, userID, actorUserID string, payload map[string]any) error
}

// Deps are the Handler's collaborators.
type Deps struct {
	Tenants  *tenantresolve.Resolver
	Auth     *authcheck.Checker
	Registry Registry
	Settings Settings
	Cache    *cache.Client
	Hub      *ws.Hub
	Audit    AuditEmitter
}

type Handler struct {
	Deps
}

func NewHandler(deps Deps) *Handler {
	return &Handler{Deps: deps}
}

type permissionJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

type moduleJSON struct {
	Name        string           `json:"name"`
	DisplayName string           `json:"display_name"`
	Description string           `json:"description"`
	Version     string           `json:"version"`
	Type        string           `json:"type"`
	Entitled    bool             `json:"entitled"`
	Enabled     bool             `json:"enabled"`
	DependsOn   []string         `json:"depends_on"`
	Permissions []permissionJSON `json:"permissions"`
}

type settingsRequest struct {
	Enabled *bool `json:"enabled"`
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
	tenantID     string
	tenantSlug   string
	userID       string
	entitlements tenantresolve.EntitlementSet
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
	return caller{
		tenantID:     tenantCtx.TenantID,
		tenantSlug:   tenantCtx.Slug,
		userID:       authCtx.UserID,
		entitlements: tenantCtx.Entitlements,
	}, true
}

func internalError(w http.ResponseWriter, c caller, operation string, err error) {
	log.Error().Err(err).Str("tenant", c.tenantSlug).Msg("adminmodules: " + operation)
	writeError(w, http.StatusInternalServerError, "internal_error", "request failed")
}

func summarize(m *module.LoadedModule, e tenantresolve.EntitlementSet) moduleJSON {
	name := m.Manifest.Name
	perms := make([]permissionJSON, len(m.Manifest.Permissions))
	for i, p := range m.Manifest.Permissions {
		perms[i] = permissionJSON{Name: p.Name, Description: p.Description, Category: cmp.Or(p.Category, m.Manifest.DisplayName)}
	}
	dependsOn := slices.Clone(m.Manifest.DependsOn)
	if dependsOn == nil {
		dependsOn = []string{}
	}
	return moduleJSON{
		Name:        name,
		DisplayName: m.Manifest.DisplayName,
		Description: m.Manifest.Description,
		Version:     m.Manifest.Version,
		Type:        m.Manifest.Type,
		Entitled:    e.ModuleEntitled(name),
		Enabled:     e.ModuleEnabled(name),
		DependsOn:   dependsOn,
		Permissions: perms,
	}
}

// ServeList is GET /admin/modules.
func (h *Handler) ServeList(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	snap := h.Registry.Snapshot()
	if snap == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}

	modules := []moduleJSON{}
	for _, m := range snap.Modules() {
		if m.Status != module.StatusReady {
			continue
		}
		modules = append(modules, summarize(m, c.entitlements))
	}
	slices.SortFunc(modules, func(a, b moduleJSON) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)), cmp.Compare(a.Name, b.Name))
	})

	writeJSON(w, http.StatusOK, map[string]any{"modules": modules})
}

// ServePatchSettings is PATCH /admin/modules/{name}/settings.
func (h *Handler) ServePatchSettings(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body settingsRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil || body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "enabled is required")
		return
	}

	snap := h.Registry.Snapshot()
	if snap == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}
	name := route.ParamsFromContext(ctx)["name"]
	target, found := snap.Modules()[name]
	if !found || target.Status != module.StatusReady {
		writeError(w, http.StatusNotFound, "not_found", "module is not installed")
		return
	}
	if !c.entitlements.ModuleEntitled(name) {
		writeError(w, http.StatusConflict, "module_not_entitled", "the current plan does not include this module")
		return
	}

	enabled := *body.Enabled
	if enabled != c.entitlements.ModuleEnabled(name) {
		if conflict := dependencyConflict(snap, c.entitlements, name, enabled); conflict != nil {
			writeErrorDetails(w, http.StatusConflict, conflict.code, conflict.message, map[string]any{"modules": conflict.modules})
			return
		}

		var disabledBy *string
		if !enabled {
			disabledBy = &c.userID
		}
		if err := h.Settings.SetModuleEnabledForTenant(ctx, c.tenantID, name, enabled, disabledBy); err != nil {
			internalError(w, c, "set module enabled", err)
			return
		}
		h.afterChange(ctx, c, name, enabled)
		c.entitlements = withModuleState(c.entitlements, name, enabled)
	}

	writeJSON(w, http.StatusOK, summarize(target, c.entitlements))
}

type conflict struct {
	code    string
	message string
	modules []string
}

// dependencyConflict reports why name cannot move to enabled: disabling
// needs every other enabled module that depends on it to be gone, and
// enabling needs everything it depends on to be enabled.
func dependencyConflict(snap *registry.RegistrySnapshot, e tenantresolve.EntitlementSet, name string, enabled bool) *conflict {
	var blocking []string
	if enabled {
		for _, dep := range snap.Modules()[name].Manifest.DependsOn {
			if !e.ModuleEnabled(dep) {
				blocking = append(blocking, dep)
			}
		}
		if len(blocking) == 0 {
			return nil
		}
		slices.Sort(blocking)
		return &conflict{"module_dependency_disabled", "enable the modules this module depends on first", blocking}
	}

	for other, m := range snap.Modules() {
		if m.Status == module.StatusReady && e.ModuleEnabled(other) && slices.Contains(m.Manifest.DependsOn, name) {
			blocking = append(blocking, other)
		}
	}
	if len(blocking) == 0 {
		return nil
	}
	slices.Sort(blocking)
	return &conflict{"module_has_dependents", "disable the modules that depend on this module first", blocking}
}

// withModuleState returns e as the engine would resolve it after the module
// was enabled or disabled, so the response reflects the write without a
// second resolution.
func withModuleState(e tenantresolve.EntitlementSet, name string, enabled bool) tenantresolve.EntitlementSet {
	e.Features = cloneOrNew(e.Features)
	e.Disabled = cloneOrNew(e.Disabled)
	e.Features["module."+name] = enabled
	if enabled {
		delete(e.Disabled, name)
	} else {
		e.Disabled[name] = true
	}
	return e
}

func cloneOrNew(m map[string]bool) map[string]bool {
	if m == nil {
		return map[string]bool{}
	}
	return maps.Clone(m)
}

// afterChange is best-effort throughout: the setting already committed, so
// a failure here only delays the change until the entitlement cache
// expires or the session next loads.
func (h *Handler) afterChange(ctx context.Context, c caller, name string, enabled bool) {
	if err := h.Cache.Delete(ctx, tenantresolve.EntitlementCacheKey(c.tenantID)); err != nil {
		log.Warn().Err(err).Str("tenant", c.tenantSlug).Msg("adminmodules: entitlement cache invalidation failed")
	}

	if h.Hub != nil {
		payload := map[string]any{"module": name, "enabled": enabled}
		if _, err := h.Hub.Broadcast(ctx, ws.TenantChannel(c.tenantID), modulesChangedEvent, payload); err != nil {
			log.Warn().Err(err).Str("tenant", c.tenantSlug).Msg("adminmodules: broadcast failed")
		}
	}

	event := "module.disabled"
	if enabled {
		event = "module.enabled"
	}
	if h.Audit == nil {
		log.Warn().Str("tenant", c.tenantSlug).Str("event", event).Msg("adminmodules: no audit emitter wired, event not recorded")
		return
	}
	if err := h.Audit.Emit(ctx, c.tenantSlug, event, "", c.userID, map[string]any{"module": name}); err != nil {
		log.Warn().Err(err).Str("tenant", c.tenantSlug).Str("event", event).Msg("adminmodules: audit emit failed")
	}
}
