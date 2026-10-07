package module

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func init() {
	registerCollector(permissionsCollector{})
	registerCollector(policiesCollector{})
	registerCollector(usesPermissionsCollector{})
}

// permissionBlocks are the hand-written manifest keys whose permission names
// the permissions collector checks, since JSON cannot take a perm.Permission.
var permissionBlocks = []string{"views", "navigation", "reports"}

// permissionsCollector generates permissions from perm.Define declarations
// (manifest-spec.md §7). It also checks every permission name in the
// hand-written views, navigation and reports against the module's defined and
// referenced set.
type permissionsCollector struct{}

func (permissionsCollector) Key() string { return "permissions" }

func (permissionsCollector) Kinds() []string {
	return []string{perm.KindPermission, perm.KindPermissionRef}
}

func (permissionsCollector) Collect(d Declarations, info ModuleInfo) (any, error) {
	defined, refs, err := decodePermissions(d)
	if err != nil {
		return nil, err
	}

	problems := checkPermissionOwnership(defined, refs, info.Name)
	known := make(map[string]bool, len(defined)+len(refs))
	out := make([]manifest.Permission, 0, len(defined))
	for _, p := range defined {
		known[p.Name] = true
		out = append(out, manifest.Permission{
			Name:         p.Name,
			Description:  p.Description,
			Category:     p.Category,
			DefaultRoles: p.DefaultRoles,
		})
	}
	for _, r := range refs {
		known[r.Name] = true
	}
	problems = append(problems, lintPermissionNames(info.Manifest, known)...)
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	slices.SortFunc(out, func(a, b manifest.Permission) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

// policiesCollector generates policies from perm.DefinePolicy declarations
// (manifest-spec.md §8).
type policiesCollector struct{}

func (policiesCollector) Key() string { return "policies" }

func (policiesCollector) Kinds() []string {
	return []string{perm.KindPolicy, perm.KindPermission, perm.KindPermissionRef}
}

func (policiesCollector) Collect(d Declarations, info ModuleInfo) (any, error) {
	policies, err := decodeDeclarations[perm.PolicyDeclaration](d, perm.KindPolicy)
	if err != nil {
		return nil, err
	}
	defined, refs, err := decodePermissions(d)
	if err != nil {
		return nil, err
	}

	known := make(map[string]bool, len(defined)+len(refs))
	for _, p := range defined {
		known[p.Name] = true
	}
	for _, r := range refs {
		known[r.Name] = true
	}

	var problems []error
	seen := make(map[string]bool, len(policies))
	out := make([]manifest.Policy, 0, len(policies))
	for _, p := range policies {
		if owner, _, _ := strings.Cut(p.Name, ":"); owner != info.Name {
			problems = append(problems, fmt.Errorf("policy %q: perm.DefinePolicy module segment %q must be the module's own name %q", p.Name, owner, info.Name))
		}
		if seen[p.Name] {
			problems = append(problems, fmt.Errorf("policy %q is declared more than once with perm.DefinePolicy", p.Name))
		}
		seen[p.Name] = true
		if !known[p.AppliesTo] {
			problems = append(problems, fmt.Errorf("policy %q applies to %q, which the module neither defines with perm.Define nor references with perm.Ref", p.Name, p.AppliesTo))
		}
		out = append(out, manifest.Policy{
			Name:        p.Name,
			Description: p.Description,
			AppliesTo:   p.AppliesTo,
			Condition:   p.Condition,
		})
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	slices.SortFunc(out, func(a, b manifest.Policy) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

// usesPermissionsCollector generates uses_permissions from perm.Ref
// declarations (manifest-spec.md §7).
type usesPermissionsCollector struct{}

func (usesPermissionsCollector) Key() string { return "uses_permissions" }

func (usesPermissionsCollector) Kinds() []string { return []string{perm.KindPermissionRef} }

func (usesPermissionsCollector) Collect(d Declarations, _ ModuleInfo) (any, error) {
	refs, err := decodeDeclarations[perm.RefDeclaration](d, perm.KindPermissionRef)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Name)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func decodePermissions(d Declarations) ([]perm.PermissionDeclaration, []perm.RefDeclaration, error) {
	defined, err := decodeDeclarations[perm.PermissionDeclaration](d, perm.KindPermission)
	if err != nil {
		return nil, nil, err
	}
	refs, err := decodeDeclarations[perm.RefDeclaration](d, perm.KindPermissionRef)
	if err != nil {
		return nil, nil, err
	}
	return defined, refs, nil
}

// checkPermissionOwnership checks that each perm.Define names a permission of
// module and each perm.Ref one of another module, and that no name is defined
// twice or both defined and referenced.
func checkPermissionOwnership(defined []perm.PermissionDeclaration, refs []perm.RefDeclaration, module string) []error {
	var problems []error
	seen := make(map[string]bool, len(defined))
	for _, p := range defined {
		if owner, _, _ := strings.Cut(p.Name, ":"); owner != module {
			problems = append(problems, fmt.Errorf("permission %q: perm.Define module segment %q must be the module's own name %q", p.Name, owner, module))
		}
		if seen[p.Name] {
			problems = append(problems, fmt.Errorf("permission %q is declared more than once with perm.Define", p.Name))
		}
		seen[p.Name] = true
	}
	for _, r := range refs {
		if owner, _, _ := strings.Cut(r.Name, ":"); owner == module {
			problems = append(problems, fmt.Errorf("permission %q: perm.Ref names the module's own permission; declare it with perm.Define", r.Name))
		}
		if seen[r.Name] {
			problems = append(problems, fmt.Errorf("permission %q is both defined with perm.Define and referenced with perm.Ref", r.Name))
		}
	}
	return problems
}

// lintPermissionNames reports each value of a "permission" key and each
// element of a "permissions" array in the hand-written views, navigation and
// reports that is not in known. Each report names the block, its entry and
// the JSON path inside the entry.
func lintPermissionNames(manifestDoc map[string]any, known map[string]bool) []error {
	var problems []error
	for _, key := range permissionBlocks {
		entries, _ := manifestDoc[key].([]any)
		for i, entry := range entries {
			where := fmt.Sprintf("%s[%d]", key, i)
			if obj, ok := entry.(map[string]any); ok {
				for _, field := range []string{"name", "label"} {
					if name, _ := obj[field].(string); name != "" {
						where = fmt.Sprintf("%s %q", strings.TrimSuffix(key, "s"), name)
						break
					}
				}
			}
			walkPermissionNames(entry, "", func(path, name string) {
				if !known[name] {
					problems = append(problems, fmt.Errorf("%s: %s names permission %q, which the module neither defines with perm.Define nor references with perm.Ref", where, path, name))
				}
			})
		}
	}
	return problems
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
