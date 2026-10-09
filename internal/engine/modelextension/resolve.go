// Package modelextension validates additive declarations and resolves effective model shapes.
package modelextension

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// Validate checks the extension's authority before any tenant DDL is attempted.
func Validate(m *module.LoadedModule) error {
	if m.Manifest.Type == "field_extension" && len(m.ModelDecls) > 0 {
		return fmt.Errorf("field_extension modules declare Schema.Extensions rather than owned models")
	}
	if m.Manifest.Type == "field_extension" && (m.Manifest.Schema.ExtendsModule == nil ||
		len(m.Manifest.Schema.ExtendsModels) == 0 || !slices.Contains(m.Manifest.DependsOn, *m.Manifest.Schema.ExtendsModule)) {
		return fmt.Errorf("field_extension targets %q require schema.extends_module, schema.extends_models and the owner in depends_on", m.Manifest.Schema.ExtendsModels)
	}
	for _, ext := range m.ModelExtensions {
		owner, resource, qualified := strings.Cut(ext.Model, ".")
		if m.Manifest.Type != "field_extension" || !qualified || resource == "" ||
			m.Manifest.Schema.ExtendsModule == nil || owner != *m.Manifest.Schema.ExtendsModule ||
			!slices.Contains(m.Manifest.Schema.ExtendsModels, ext.Model) ||
			!slices.Contains(m.Manifest.DependsOn, owner) {
			return fmt.Errorf("model extension %q must name a schema.extends_models target owned by schema.extends_module in depends_on", ext.Model)
		}

		for _, field := range ext.Fields {
			def := field.Def
			if def.IsPrimaryKey || def.IsPrimary || len(def.WorkflowTransitions) > 0 {
				return fmt.Errorf("model extension %q field %q cannot change model identity or declare workflow routes", ext.Model, field.Name)
			}
			if def.IsRequired && def.DefaultExpr == nil {
				return fmt.Errorf("model extension %q field %q must be nullable or have a default", ext.Model, field.Name)
			}
		}
	}

	return nil
}

// Resolve builds effective model declarations without changing table ownership or
// mutating declarations retained by existing registry snapshots.
func Resolve(modules map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
	resolved := make(map[string]*module.LoadedModule, len(modules))
	for name, m := range modules {
		copyModule := *m
		copyModule.ModelDecls = slices.Clone(m.ModelDecls)
		copyModule.TypeDecls = slices.Clone(m.TypeDecls)
		for i := range copyModule.ModelDecls {
			copyModule.ModelDecls[i].Fields = slices.Clone(m.ModelDecls[i].Fields)
			copyModule.ModelDecls[i].Indexes = slices.Clone(m.ModelDecls[i].Indexes)
		}
		resolved[name] = &copyModule
	}

	names, err := Order(modules)
	if err != nil {
		return nil, err
	}

	for _, name := range names {
		m := modules[name]
		if m.Status == module.StatusFailed {
			continue
		}
		if err := Validate(m); err != nil {
			return nil, fmt.Errorf("module %q: %w", name, err)
		}
		if m.Manifest.Type == "field_extension" {
			for _, dependency := range m.Manifest.DependsOn {
				if dep := modules[dependency]; dep == nil || dep.Status == module.StatusFailed {
					return nil, fmt.Errorf("module %q: model extension dependency %q is not loaded", name, dependency)
				}
			}
			for _, target := range m.Manifest.Schema.ExtendsModels {
				owner, _, _ := strings.Cut(target, ".")
				base := modules[owner]
				if owner != *m.Manifest.Schema.ExtendsModule || base == nil || base.Status == module.StatusFailed ||
					!slices.Contains(base.Manifest.Schema.OwnedModels, target) ||
					!slices.ContainsFunc(base.ModelDecls, func(md model.ModelDeclaration) bool { return md.QualifiedName(owner) == target && md.Backend == "" }) {
					return nil, fmt.Errorf("module %q: model extension target %q must be owned by the loaded, table-backed extends_module", name, target)
				}
			}
		}

		for _, ext := range m.ModelExtensions {
			owner, _, _ := strings.Cut(ext.Model, ".")
			base := resolved[owner]
			if base == nil || base.Status == module.StatusFailed || !slices.Contains(base.Manifest.Schema.OwnedModels, ext.Model) {
				return nil, fmt.Errorf("module %q: model extension target %q is not owned by a loaded module", name, ext.Model)
			}

			idx := slices.IndexFunc(base.ModelDecls, func(md model.ModelDeclaration) bool {
				return md.QualifiedName(owner) == ext.Model
			})
			if idx < 0 || base.ModelDecls[idx].Backend != "" {
				return nil, fmt.Errorf("module %q: model extension target %q must be table-backed", name, ext.Model)
			}

			md := &base.ModelDecls[idx]
			for _, declaration := range m.TypeDecls {
				position := slices.IndexFunc(base.TypeDecls, func(t model.TypeDeclaration) bool { return t.Name == declaration.Name })
				if position < 0 {
					base.TypeDecls = append(base.TypeDecls, declaration)
				} else if !slices.Equal(base.TypeDecls[position].Values, declaration.Values) {
					return nil, fmt.Errorf("module %q: model extension %q cannot redefine enum type %q", name, ext.Model, declaration.Name)
				}
			}
			for _, field := range ext.Fields {
				if field.Name == "" || slices.ContainsFunc(md.Fields, func(f model.NamedField) bool { return f.Name == field.Name }) {
					return nil, fmt.Errorf("module %q: model extension %q field %q already exists or has an empty name", name, ext.Model, field.Name)
				}
				field.DeclaringModule = name
				md.Fields = append(md.Fields, field)
			}

			for _, index := range ext.Indexes {
				if index.Name == "" || slices.ContainsFunc(md.Indexes, func(i model.NamedIndex) bool { return i.Name == index.Name }) {
					return nil, fmt.Errorf("module %q: model extension %q index %q already exists or has an empty name", name, ext.Model, index.Name)
				}
				for _, column := range index.Def.Columns {
					if !slices.ContainsFunc(md.Fields, func(f model.NamedField) bool { return f.Name == column }) {
						return nil, fmt.Errorf("module %q: model extension %q index %q names unknown field %q", name, ext.Model, index.Name, column)
					}
				}
				md.Indexes = append(md.Indexes, index)
			}
		}
	}

	return resolved, nil
}

func Order(modules map[string]*module.LoadedModule) ([]string, error) {
	names := slices.Sorted(maps.Keys(modules))
	slices.SortStableFunc(names, func(a, b string) int {
		return cmp.Compare(modules[a].LoadOrder, modules[b].LoadOrder)
	})
	names = slices.DeleteFunc(names, func(name string) bool { return modules[name].Status == module.StatusFailed })
	loaded := make(map[string]bool, len(names))
	ordered := make([]string, 0, len(names))

	for len(names) > 0 {
		index := slices.IndexFunc(names, func(name string) bool {
			for _, dependency := range modules[name].Manifest.DependsOn {
				dep := modules[dependency]
				if dep != nil && dep.Status != module.StatusFailed && !loaded[dependency] {
					return false
				}
			}

			return true
		})
		if index < 0 {
			return nil, fmt.Errorf("model extension dependency cycle at module %q", names[0])
		}

		name := names[index]
		loaded[name] = true
		ordered = append(ordered, name)
		names = slices.Delete(names, index, index+1)
	}

	return ordered, nil
}
