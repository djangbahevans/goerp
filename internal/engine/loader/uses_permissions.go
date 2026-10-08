package loader

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/rs/zerolog/log"
)

// ValidateUsesPermissions checks every module's permission references
// against the permissions the loaded modules declare (manifest-spec.md §7,
// §28):
//
//   - The owner of each uses_permissions entry must be a depends_on or
//     soft_depends_on module. A loaded owner must declare the permission. A
//     soft dependency that is not loaded leaves the entry unresolved with a
//     warning; no role can hold the permission, so it denies.
//   - Every permission a route, EnableOps operation, workflow transition, field access
//     declaration, view, navigation item or report names must appear in the
//     module's own permissions or uses_permissions.
//
// A violation fails the referencing module. Exported so a caller loading
// modules one at a time (not via LoadAll) can run the same validation once
// its own loop finishes, the same pattern as ValidateUsesConfig.
func ValidateUsesPermissions(modules map[string]*module.LoadedModule) {
	// Owners are judged by their status on entry, so a module this pass
	// fails does not change the outcome for modules visited after it.
	usable := make(map[string]bool, len(modules))
	for name, m := range modules {
		usable[name] = m.Status != module.StatusFailed
	}

	for _, name := range slices.Sorted(maps.Keys(modules)) {
		m := modules[name]
		if m.Status == module.StatusFailed {
			continue
		}

		if reason := checkUsesPermissionOwners(name, m, modules, usable); reason != "" {
			m.Fail(reason)
			continue
		}
		if reason := checkPermissionReferences(m); reason != "" {
			m.Fail(reason)
		}
	}
}

func checkUsesPermissionOwners(name string, m *module.LoadedModule, modules map[string]*module.LoadedModule, usable map[string]bool) string {
	for _, perm := range m.Manifest.UsesPermissions {
		owner, _, _ := strings.Cut(perm, ":")
		soft := slices.Contains(m.Manifest.SoftDependsOn, owner)
		if !soft && !slices.Contains(m.Manifest.DependsOn, owner) {
			return fmt.Sprintf("uses_permissions: %q is owned by module %q, which is in neither depends_on nor soft_depends_on", perm, owner)
		}

		if !usable[owner] {
			if !soft {
				return fmt.Sprintf("uses_permissions: %q targets module %q, which is not loaded", perm, owner)
			}
			log.Warn().Str("module", name).Str("permission", perm).
				Msg("uses_permissions entry is owned by a soft dependency that is not loaded; the permission denies")
			continue
		}

		if !declaresPermission(modules[owner], perm) {
			return fmt.Sprintf("uses_permissions: module %q declares no permission %q", owner, perm)
		}
	}
	return ""
}

func declaresPermission(m *module.LoadedModule, name string) bool {
	return slices.ContainsFunc(m.Manifest.Permissions, func(p manifest.Permission) bool { return p.Name == name })
}

func checkPermissionReferences(m *module.LoadedModule) string {
	allowed := make(map[string]bool, len(m.Manifest.Permissions)+len(m.Manifest.UsesPermissions))
	for _, p := range m.Manifest.Permissions {
		allowed[p.Name] = true
	}
	for _, name := range m.Manifest.UsesPermissions {
		allowed[name] = true
	}

	for _, ref := range permissionReferences(m) {
		if !allowed[ref.name] {
			return fmt.Sprintf("%s names permission %q, which this module neither declares in permissions nor lists in uses_permissions", ref.where, ref.name)
		}
	}
	return ""
}

type permissionReference struct {
	name  string
	where string
}

// permissionReferences lists every permission name m's routes, EnableOps operations, workflow
// transitions, field access declarations, views, navigation and reports
// carry, with a description of where each was named.
func permissionReferences(m *module.LoadedModule) []permissionReference {
	var refs []permissionReference
	add := func(name, where string) {
		if name != "" {
			refs = append(refs, permissionReference{name: name, where: where})
		}
	}

	for _, r := range m.ExplicitRoutes {
		where := fmt.Sprintf("route %s %s", r.Method, r.Path)
		if r.Name != "" {
			where = fmt.Sprintf("action %q (route %s %s)", r.Name, r.Method, r.Path)
		}
		for _, p := range r.Permissions {
			add(p, where)
		}
	}

	for _, md := range m.ModelDecls {
		for _, op := range md.EnabledOps {
			add(op.Permission, fmt.Sprintf("model %s EnableOps %q", md.Name, op.Name))
		}

		for _, f := range md.Fields {
			site := fmt.Sprintf("model %s field %s", md.Name, f.Name)
			add(f.Def.ReadPermission, site+" read access")
			add(f.Def.WritePermission, site+" write access")
			for _, t := range f.Def.WorkflowTransitions {
				add(t.Permission, fmt.Sprintf("%s workflow transition %q", site, t.ActionName))
			}
		}
	}

	for _, block := range []struct {
		key    string
		entity string
		value  any
	}{
		{"views", "view", m.Manifest.Views},
		{"navigation", "navigation group", m.Manifest.Navigation},
		{"reports", "report", m.Manifest.Reports},
	} {
		refs = append(refs, manifestBlockPermissions(block.key, block.entity, block.value)...)
	}
	return refs
}

// manifestBlockPermissions walks a manifest block's JSON form for every
// "permission" string and "permissions" array element, naming the owning
// entry by its name or label.
func manifestBlockPermissions(key, entity string, block any) []permissionReference {
	raw, err := json.Marshal(block)
	if err != nil {
		return nil
	}
	var entries []any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil
	}

	var refs []permissionReference
	for i, entry := range entries {
		where := fmt.Sprintf("%s[%d]", key, i)
		if obj, ok := entry.(map[string]any); ok {
			for _, field := range []string{"name", "label"} {
				if name, _ := obj[field].(string); name != "" {
					where = fmt.Sprintf("%s %q", entity, name)
					break
				}
			}
		}
		walkPermissionNames(entry, "", func(path, name string) {
			refs = append(refs, permissionReference{name: name, where: fmt.Sprintf("%s: %s", where, path)})
		})
	}
	return refs
}

func walkPermissionNames(v any, path string, visit func(path, name string)) {
	switch v := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(v)) {
			child := k
			if path != "" {
				child = path + "." + k
			}
			switch val := v[k].(type) {
			case string:
				if k == "permission" && val != "" {
					visit(child, val)
				}
			case []any:
				if k == "permissions" {
					for i, el := range val {
						if name, ok := el.(string); ok && name != "" {
							visit(fmt.Sprintf("%s[%d]", child, i), name)
						}
					}
					continue
				}
				walkPermissionNames(val, child, visit)
			default:
				walkPermissionNames(val, child, visit)
			}
		}
	case []any:
		for i, el := range v {
			walkPermissionNames(el, fmt.Sprintf("%s[%d]", path, i), visit)
		}
	}
}
