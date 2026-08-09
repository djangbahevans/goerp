package permission

import "github.com/djangbahevans/goerp/internal/engine/manifest"

// PermissionRegistry assigns each declared permission a stable bitfield
// index at module load time. Indices are never reused after a module
// unloads — nextIdx only ever increases, so an unloaded module's positions
// simply go unused until the engine process restarts.
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
