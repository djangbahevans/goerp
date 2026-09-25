package permcache

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/rs/zerolog/log"
)

// RolePermissionMap is auth-internals.md §14's cache layer 3 — the
// in-process role → permission bitfield map, resolved against the current
// process's own permission.PermissionRegistry index assignments. The doc
// describes this as a sync.Map; this instead follows the idiom this
// codebase already uses for exactly this "built fresh, read frequently,
// swapped wholesale on rebuild" shape of data — registry.ModuleRegistry's
// atomic.Pointer[RegistrySnapshot] — so a reader never observes a
// half-rebuilt map.
type RolePermissionMap struct {
	current atomic.Pointer[roleMapSnapshot]
	// writeMu serializes RebuildAll and RebuildTenant, so a tenant rebuild
	// can't overwrite a concurrent full rebuild with its older copy.
	writeMu sync.Mutex
}

type roleMapSnapshot struct {
	bits     map[string]permission.PermissionBitfield // keyed by role_id
	byTenant map[string][]string                      // tenant slug → its role ids
}

func NewRolePermissionMap() *RolePermissionMap {
	m := &RolePermissionMap{}
	m.current.Store(&roleMapSnapshot{bits: map[string]permission.PermissionBitfield{}, byTenant: map[string][]string{}})
	return m
}

// Lookup returns roleID's resolved (inheritance-merged) permission
// bitfield, and whether that role is currently known.
func (m *RolePermissionMap) Lookup(roleID string) (permission.PermissionBitfield, bool) {
	bits, ok := m.current.Load().bits[roleID]
	return bits, ok
}

// RebuildAll rebuilds the whole map from scratch, across every active
// tenant, and swaps it in atomically once fully built. Called right after
// registry.ModuleRegistry.Update/UpdateWithLocked rebuilds the permission
// registry (engine.go's startup sequence, moduleinstall.Worker's publish
// step, and modulereload.Leader's own publish step) — this map has to be
// rebuilt in lockstep, since a stale map would resolve bitfields against
// index assignments that may no longer match modulePerms. Every caller
// that runs concurrently with another writer (Worker, Leader) calls this
// while still holding the same *registry.ModuleRegistry's Lock it used for
// its own UpdateWithLocked call — this function does no locking of its
// own, so two overlapping RebuildAll calls for two different writers could
// otherwise interleave and have whichever's DB queries happen to finish
// last silently overwrite the other's more current result, regardless of
// which one actually published last.
//
// Only tenants.ActiveTenants failing outright is fatal (the tenant list
// itself couldn't be enumerated — a real infra problem). A single
// tenant's AllRoles/AllRolePermissions query failing is logged and
// skipped, not fatal, matching tenantsync.SyncAll's established
// per-tenant failure-isolation philosophy elsewhere in this same startup
// sequence.
func (m *RolePermissionMap) RebuildAll(ctx context.Context, tenants *tenant.Store, roles *role.Store, reg *permission.PermissionRegistry) error {
	return m.Resync(ctx, tenants, roles, func() *permission.PermissionRegistry { return reg })
}

// Resync is RebuildAll with the registry read under the write lock, for a
// caller that doesn't hold the module registry's lock (Listener, catching
// up on changes announced while it wasn't listening).
func (m *RolePermissionMap) Resync(ctx context.Context, tenants *tenant.Store, roles *role.Store, currentRegistry func() *permission.PermissionRegistry) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	reg := currentRegistry()

	active, err := tenants.ActiveTenants(ctx)
	if err != nil {
		return fmt.Errorf("list active tenants: %w", err)
	}

	built := &roleMapSnapshot{bits: map[string]permission.PermissionBitfield{}, byTenant: map[string][]string{}}
	for _, t := range active {
		ids, err := buildTenantRoles(ctx, roles, reg, t.Slug, built.bits)
		if err != nil {
			log.Warn().Err(err).Str("tenant", t.Slug).Msg("permcache: failed to build role permission map for tenant, skipping")
			continue
		}
		built.byTenant[t.Slug] = ids
	}

	m.current.Store(built)
	return nil
}

// RebuildTenant re-resolves one tenant's roles and swaps them in, leaving
// every other tenant's entries as they are: a tenant admin's role create,
// update or delete (internal/engine/auth/adminroles), and Listener's
// response on every replica to one being announced. reg is called under
// the same lock RebuildAll holds, so it must return the current registry
// snapshot's permission registry rather than one captured earlier.
func (m *RolePermissionMap) RebuildTenant(ctx context.Context, roles *role.Store, reg func() *permission.PermissionRegistry, tenantSlug string) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	fresh := map[string]permission.PermissionBitfield{}
	ids, err := buildTenantRoles(ctx, roles, reg(), tenantSlug, fresh)
	if err != nil {
		return fmt.Errorf("rebuild role permission map for tenant %s: %w", tenantSlug, err)
	}

	old := m.current.Load()
	next := &roleMapSnapshot{bits: maps.Clone(old.bits), byTenant: maps.Clone(old.byTenant)}
	for _, id := range old.byTenant[tenantSlug] {
		delete(next.bits, id)
	}
	maps.Copy(next.bits, fresh)
	next.byTenant[tenantSlug] = ids

	m.current.Store(next)
	return nil
}

// buildTenantRoles resolves every role in tenantSlug's schema into dest,
// keyed by role_id, and returns the tenant's role ids. Role IDs are
// uuidv7()-generated per tenant row, never shared across tenants, so a flat
// map across every tenant's roles never collides.
func buildTenantRoles(ctx context.Context, roles *role.Store, reg *permission.PermissionRegistry, tenantSlug string, dest map[string]permission.PermissionBitfield) ([]string, error) {
	allRoles, err := roles.AllRoles(ctx, tenantSlug)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	if len(allRoles) == 0 {
		return []string{}, nil
	}

	rolePerms, err := roles.AllRolePermissions(ctx, tenantSlug)
	if err != nil {
		return nil, fmt.Errorf("list role permissions: %w", err)
	}

	byID := make(map[string]role.Role, len(allRoles))
	ids := make([]string, 0, len(allRoles))
	for _, r := range allRoles {
		byID[r.ID] = r
		ids = append(ids, r.ID)
	}

	resolving := map[string]bool{} // cycle guard — auth-internals.md §10 says cycles are rejected at role-creation time; defensive, not load-bearing
	for _, r := range allRoles {
		// resolveRoleBitfield writes dest[r.ID] itself once resolved
		// (its own memoization) — the error here is only reachable for
		// a cycle or a dangling parent_id reference.
		if _, err := resolveRoleBitfield(r.ID, byID, rolePerms, reg, dest, resolving); err != nil {
			log.Warn().Err(err).Str("tenant", tenantSlug).Str("role", r.Name).Msg("permcache: failed to resolve role, skipping")
		}
	}

	return ids, nil
}

// resolveRoleBitfield resolves roleID's own grants OR'd with its parent's
// already-resolved bitfield (auth-internals.md §10 "Role inheritance" —
// additive, single-parent). Memoized via dest: once a role is resolved
// it's read back from dest rather than recomputed if another role
// inherits from it too.
func resolveRoleBitfield(
	roleID string,
	byID map[string]role.Role,
	rolePerms map[string][]string,
	reg *permission.PermissionRegistry,
	dest map[string]permission.PermissionBitfield,
	resolving map[string]bool,
) (permission.PermissionBitfield, error) {
	if bits, ok := dest[roleID]; ok {
		return bits, nil
	}
	if resolving[roleID] {
		return nil, fmt.Errorf("cycle detected resolving role %q", roleID)
	}
	resolving[roleID] = true
	defer delete(resolving, roleID)

	r, ok := byID[roleID]
	if !ok {
		return nil, fmt.Errorf("role %q referenced as a parent but not found", roleID)
	}

	var bits permission.PermissionBitfield
	if r.ParentID != nil {
		parentBits, err := resolveRoleBitfield(*r.ParentID, byID, rolePerms, reg, dest, resolving)
		if err != nil {
			return nil, err
		}
		bits.Or(parentBits)
	}

	for _, name := range rolePerms[roleID] {
		idx, ok := reg.Index(name)
		if !ok {
			// auth-internals.md §10 "Permission naming": "Unknown
			// permission names in role assignments... generate a
			// load-time warning" — not a fatal error for the role.
			log.Warn().Str("role", roleID).Str("permission", name).Msg("permcache: unknown permission name granted to role, skipping")
			continue
		}
		bits.Set(idx)
	}

	dest[roleID] = bits
	return bits, nil
}
