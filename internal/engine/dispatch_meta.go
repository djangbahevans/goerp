package engine

import (
	"net/http"
	"sort"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
)

// metaPermissionsResponse is GET /_meta/permissions' response shape —
// auth-internals.md's own worked example under "The /_meta/permissions
// endpoint" is the canonical response ({permissions, field_access} only);
// modules_enabled is multitenancy-internals.md §8 "Navigation
// filtering"'s addition, not part of that canonical example.
type metaPermissionsResponse struct {
	Permissions    []string                          `json:"permissions"`
	FieldAccess    map[string]map[string]fieldAccess `json:"field_access"`
	ModulesEnabled []string                          `json:"modules_enabled"`
}

type fieldAccess struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
}

// dispatchPermissionsRoute is GET /_meta/permissions' handler (goerp#417)
// — registered EngineNative but not EngineBuiltin (registry.go), so it
// rides the standard tenant/auth/permission middleware chain like any
// other Class A route (auth-internals.md §9) rather than resolving its
// own identity.
func (e *Engine) dispatchPermissionsRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		// Unreachable via the real middleware chain — routeAuthMiddleware
		// requires Auth: "required" (registry.go's registration) before
		// this handler is ever reached. Guarded for direct-call
		// testability, matching dispatchORMRoute's own identical guard.
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}

	permReg := snap.PermissionRegistry()
	permissions := []string{}
	if permReg != nil {
		for _, name := range permReg.Names() {
			if hasPermission(permReg, authCtx, name) {
				permissions = append(permissions, name)
			}
		}
	}

	fieldAccessMap := map[string]map[string]fieldAccess{}
	if fieldSecReg := snap.FieldSecRegistry(); fieldSecReg != nil {
		for modelName, fields := range fieldSecReg.AllRules() {
			for fieldName, rule := range fields {
				read := rule.ReadPermission == "" || hasPermission(permReg, authCtx, rule.ReadPermission)
				write := rule.WritePermission == "" || hasPermission(permReg, authCtx, rule.WritePermission)
				if fieldAccessMap[modelName] == nil {
					fieldAccessMap[modelName] = map[string]fieldAccess{}
				}
				fieldAccessMap[modelName][fieldName] = fieldAccess{Read: read, Write: write}
			}
		}
	}

	modulesEnabled := []string{}
	for name, m := range snap.Modules() {
		if m.Status == module.StatusReady && tenantCtx.Entitlements.ModuleEnabled(name) {
			modulesEnabled = append(modulesEnabled, name)
		}
	}
	sort.Strings(modulesEnabled)

	writeJSON(w, http.StatusOK, metaPermissionsResponse{
		Permissions:    permissions,
		FieldAccess:    fieldAccessMap,
		ModulesEnabled: modulesEnabled,
	})
}

// hasPermission reports whether authCtx's caller holds permissionName,
// resolved against permReg's stable bitfield index — mirrors
// internal/engine/wasm/host_orm.go's unexported callerHasPermission,
// rewritten against the bare authcheck.AuthContext this package's
// dispatch handlers use rather than *wasm.ModuleContext (a different
// package, and callerHasPermission is unexported). Fails closed on a nil
// registry or unregistered name, same as that original.
func hasPermission(permReg *permission.PermissionRegistry, authCtx *authcheck.AuthContext, permissionName string) bool {
	if permReg == nil {
		return false
	}
	idx, ok := permReg.Index(permissionName)
	if !ok {
		return false
	}
	return authCtx.PermissionSet.Has(idx)
}
