package permission

import (
	"maps"
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// ManageViewOverrides grants tenant-wide access to presentational view overrides.
const ManageViewOverrides = "engine.manage_view_overrides"

func Builtin() []manifest.Permission {
	return []manifest.Permission{{
		Name:         ManageViewOverrides,
		Description:  "Manage tenant view overrides",
		Category:     "Views",
		DefaultRoles: []string{"admin"},
	}}
}

// Permission indexes remain reserved after unload so existing bitfields retain their meaning.
type PermissionRegistry struct {
	indices     map[string]int // permission_name → bitfield index
	nextIdx     int
	modulePerms map[string][]manifest.Permission
}

func NewPermissionRegistry() *PermissionRegistry {
	return &PermissionRegistry{
		indices:     make(map[string]int),
		modulePerms: make(map[string][]manifest.Permission),
	}
}

// Register assigns a bitfield index to each of moduleName's declared
// permissions not already indexed, and records them for ModulePermissions.
// Calling it again for a permission name already indexed (e.g. a hot
// reload re-registering the same module) leaves that index unchanged.
func (r *PermissionRegistry) Register(moduleName string, permissions []manifest.Permission) {
	r.modulePerms[moduleName] = permissions

	for _, p := range permissions {
		if _, ok := r.indices[p.Name]; ok {
			continue
		}
		r.indices[p.Name] = r.nextIdx
		r.nextIdx++
	}
}

// Unregister drops moduleName's bookkeeping in ModulePermissions. It does
// not touch indices or nextIdx — that omission is the entire mechanism
// behind index stability: an unregistered module's indices stay assigned
// and unused rather than being freed for reuse.
func (r *PermissionRegistry) Unregister(moduleName string) {
	delete(r.modulePerms, moduleName)
}

// Index returns the stable bitfield index for permission, and whether it
// has been registered at all.
func (r *PermissionRegistry) Index(permission string) (int, bool) {
	idx, ok := r.indices[permission]
	return idx, ok
}

// ModulePermissions returns the permissions moduleName declared the last
// time it was registered.
func (r *PermissionRegistry) ModulePermissions(moduleName string) []manifest.Permission {
	return r.modulePerms[moduleName]
}

// Names returns every registered permission name, sorted — GET
// /_meta/permissions' full-catalog listing (each name then checked
// against the caller's own PermissionBitfield via Index/Has) needs to
// enumerate every name; Index alone only supports a single name→index
// lookup.
func (r *PermissionRegistry) Names() []string {
	return slices.Sorted(maps.Keys(r.indices))
}
