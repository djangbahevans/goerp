package loader

import (
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/internal/engine/modelextension"
	"github.com/djangbahevans/goerp/internal/engine/modelmeta"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func validateModelReferences(modules map[string]*module.LoadedModule) {
	resolved, err := modelextension.Resolve(modules)
	if err != nil {
		return // Model-extension validation reports the invalid declaration first.
	}

	models := make(map[string]model.ModelDeclaration)

	for name, m := range resolved {
		if m.Status != module.StatusFailed {
			for _, md := range m.ModelDecls {
				models[md.QualifiedName(name)] = md
			}
		}
	}

	validateMany2ManyModels(modules, models)

	for _, name := range validationOrder(modules) {
		m := modules[name]
		if m.Status == module.StatusFailed {
			continue
		}

		if err := checkModelReferences(resolved[name], models); err != nil {
			m.Fail(err.Error())
		}
	}
}

func checkModelReferences(m *module.LoadedModule, models map[string]model.ModelDeclaration) error {
	for _, policy := range m.Manifest.Policies {
		owner, rest, _ := strings.Cut(policy.AppliesTo, ":")
		resource, _, _ := strings.Cut(rest, ":")
		md, found := models[owner+"."+resource]
		if !found {
			return fmt.Errorf("policies: %q applies_to %q has no resolved target model", policy.Name, policy.AppliesTo)
		}

		expr, err := domain.Parse(policy.Condition)
		if err != nil {
			return fmt.Errorf("policies: %q condition: %w", policy.Name, err)
		}
		if _, err := domain.CompileToRLS(expr); err != nil {
			return fmt.Errorf("policies: %q condition: %w", policy.Name, err)
		}

		for _, field := range domain.RecordFields(expr) {
			if !hasStoredField(md, field) {
				return fmt.Errorf("policies: %q condition names unknown or unstored field record.%s on %s.%s", policy.Name, field, owner, resource)
			}
		}
	}

	for _, md := range m.ModelDecls {
		for _, field := range md.Fields {
			if field.Def.Kind == model.KindFile {
				if err := modelmeta.ValidateFile(md, field); err != nil {
					return fmt.Errorf("model %s field %s: %w", md.Name, field.Name, err)
				}
			}

			if field.DeclaringModule != "" && field.DeclaringModule != m.Manifest.Name {
				continue
			}
			for _, path := range field.Def.DependsOn {
				if err := checkComputedPath(md, path, models); err != nil {
					return fmt.Errorf("computed dependency: model %q field %q path %q: %w", md.Name, field.Name, path, err)
				}
			}
		}
	}

	for _, ext := range m.ModelExtensions {
		md := models[ext.Model]
		for _, field := range ext.Fields {
			for _, path := range field.Def.DependsOn {
				if err := checkComputedPath(md, path, models); err != nil {
					return fmt.Errorf("computed dependency: extension %q field %q path %q: %w", ext.Model, field.Name, path, err)
				}
			}
		}
	}

	return nil
}

func hasStoredField(md model.ModelDeclaration, name string) bool {
	return slices.ContainsFunc(md.Fields, func(f model.NamedField) bool {
		return f.Name == name && (!f.Def.IsComputed || f.Def.IsStored) && f.Def.Kind != model.KindOne2Many && f.Def.Kind != model.KindMany2Many
	})
}

func checkComputedPath(md model.ModelDeclaration, path string, models map[string]model.ModelDeclaration) error {
	relation, remote, hop := strings.Cut(path, ".")
	if !hop {
		if !slices.ContainsFunc(md.Fields, func(f model.NamedField) bool { return f.Name == path }) {
			return fmt.Errorf("unknown field %q", path)
		}

		return nil
	}

	if strings.Contains(remote, ".") || remote == "" {
		return fmt.Errorf("requires exactly one relation hop")
	}

	index := slices.IndexFunc(md.Fields, func(f model.NamedField) bool {
		return f.Name == relation+"_id" && f.Def.Kind == model.KindMany2One || f.Name == relation && f.Def.Kind == model.KindOne2Many
	})
	if index < 0 {
		return fmt.Errorf("%q is not a declared Many2One or One2Many relation", relation)
	}

	target, found := models[md.Fields[index].Def.RelatedModel]
	if !found || !slices.ContainsFunc(target.Fields, func(f model.NamedField) bool { return f.Name == remote }) {
		return fmt.Errorf("related model %q has no field %q", md.Fields[index].Def.RelatedModel, remote)
	}

	return nil
}
