package loader

import (
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/modelmeta"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func validateMany2ManyModels(modules map[string]*module.LoadedModule, models map[string]model.ModelDeclaration) {
	tables := map[string]string{}
	for qualified, md := range models {
		if md.Backend == "" {
			tables[modeltable.Name(md)] = qualified
		}
	}

	for _, name := range validationOrder(modules) {
		m := modules[name]
		if m.Status == module.StatusFailed {
			continue
		}

		check := func(md model.ModelDeclaration, f model.NamedField) error {
			if err := modelmeta.ValidateMany2Many(md, f); err != nil {
				return err
			}

			target, found := models[f.Def.RelatedModel]
			targetModule, _, qualified := strings.Cut(f.Def.RelatedModel, ".")
			if !qualified || !found {
				return fmt.Errorf("related_model %q is not loaded", f.Def.RelatedModel)
			}
			if targetModule != name && !slices.Contains(m.Manifest.DependsOn, targetModule) {
				return fmt.Errorf("Many2Many target %q must belong to this module or a hard dependency", f.Def.RelatedModel)
			}
			if err := modelmeta.ValidateMany2ManyTarget(target); err != nil {
				return err
			}

			if prior, found := tables[f.Def.JoinTable]; found {
				return fmt.Errorf("join table %q is already declared by %s", f.Def.JoinTable, prior)
			}
			tables[f.Def.JoinTable] = name + "." + md.ResourceName() + "." + f.Name
			return nil
		}

		for _, md := range m.ModelDecls {
			for _, f := range md.Fields {
				if f.Def.Kind != model.KindMany2Many || f.DeclaringModule != "" && f.DeclaringModule != name {
					continue
				}
				if err := check(models[md.QualifiedName(name)], f); err != nil {
					m.Fail(fmt.Sprintf("model %s field %s: %v", md.Name, f.Name, err))
					break
				}
			}
		}

		for _, ext := range m.ModelExtensions {
			for _, f := range ext.Fields {
				if f.Def.Kind != model.KindMany2Many {
					continue
				}
				if err := check(models[ext.Model], f); err != nil {
					m.Fail(fmt.Sprintf("extension %s field %s: %v", ext.Model, f.Name, err))
					break
				}
			}
		}
	}
}
