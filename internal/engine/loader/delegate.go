package loader

import (
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func validateDelegateModels(modules map[string]*module.LoadedModule, models map[string]model.ModelDeclaration) {
	for _, name := range validationOrder(modules) {
		m := modules[name]
		if m.Status == module.StatusFailed {
			continue
		}
		check := func(f model.NamedField) error {
			if f.Def.Kind != model.KindDelegate {
				return nil
			}
			owner, resource, qualified := strings.Cut(f.Def.RelatedModel, ".")
			if !qualified || resource == "" || owner != name && !slices.Contains(m.Manifest.DependsOn, owner) && !slices.Contains(m.Manifest.SoftDependsOn, owner) {
				return fmt.Errorf("delegate target %q must belong to this module or a declared dependency", f.Def.RelatedModel)
			}
			if _, found := models[f.Def.RelatedModel]; !found {
				target := modules[owner]
				if owner == name || slices.Contains(m.Manifest.DependsOn, owner) || target != nil && target.Status != module.StatusFailed {
					return fmt.Errorf("delegate target %q is not loaded", f.Def.RelatedModel)
				}
			}
			return nil
		}
		for _, md := range m.ModelDecls {
			for _, f := range md.Fields {
				if f.DeclaringModule != "" && f.DeclaringModule != name {
					continue
				}
				if err := check(f); err != nil {
					m.Fail(fmt.Sprintf("model %s field %s: %v", md.Name, f.Name, err))
				}
			}
		}
		for _, ext := range m.ModelExtensions {
			for _, f := range ext.Fields {
				if err := check(f); err != nil {
					m.Fail(fmt.Sprintf("extension %s field %s: %v", ext.Model, f.Name, err))
				}
			}
		}
	}
}
