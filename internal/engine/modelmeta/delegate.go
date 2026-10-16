package modelmeta

import (
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// ComposedFields returns a model's fields followed by unshadowed delegated fields.
// Lookup may omit an unloaded soft dependency. Cycles and ambiguous delegated
// names are invalid; a model's own fields take precedence.
func ComposedFields(md model.ModelDeclaration, lookup func(string) (model.ModelDeclaration, bool)) ([]model.NamedField, error) {
	return composedFields(md, lookup, map[string]bool{})
}

func composedFields(md model.ModelDeclaration, lookup func(string) (model.ModelDeclaration, bool), path map[string]bool) ([]model.NamedField, error) {
	fields := slices.Clone(md.Fields)
	own := make(map[string]bool, len(fields))
	inherited := map[string]bool{}
	for _, f := range fields {
		own[f.Name] = true
	}

	for _, f := range md.Fields {
		if f.Def.Kind != model.KindDelegate {
			continue
		}
		if md.Backend != "" || !strings.HasSuffix(f.Name, "_id") || f.Name == "_id" || f.Def.IsTree || f.Def.IsComputed || f.Def.IsPrimaryKey || f.Def.RelationDomain != "" {
			return nil, fmt.Errorf("delegate field %q requires a stored _id field on a Postgres model without Tree, Computed, PrimaryKey or Domain", f.Name)
		}
		if path[f.Def.RelatedModel] {
			return nil, fmt.Errorf("delegation cycle through %q", f.Def.RelatedModel)
		}
		target, ok := lookup(f.Def.RelatedModel)
		if !ok {
			continue
		}
		if target.Backend != "" || !slices.ContainsFunc(target.Fields, func(f model.NamedField) bool {
			return f.Def.IsPrimaryKey && f.Def.Kind == model.KindUUID
		}) {
			return nil, fmt.Errorf("delegate target %q must be a Postgres model with a UUID primary key", f.Def.RelatedModel)
		}

		path[f.Def.RelatedModel] = true
		targetFields, err := composedFields(target, lookup, path)
		delete(path, f.Def.RelatedModel)
		if err != nil {
			return nil, err
		}
		for _, remote := range targetFields {
			if own[remote.Name] {
				continue
			}
			if inherited[remote.Name] {
				return nil, fmt.Errorf("delegated field %q has multiple targets", remote.Name)
			}
			inherited[remote.Name] = true
			if remote.DeclaringModule == "" {
				remote.DeclaringModule, _, _ = strings.Cut(f.Def.RelatedModel, ".")
			}
			remote.Def.IsRequired = false
			remote.Def.IsPrimaryKey = false
			fields = append(fields, remote)
		}
	}
	if len(inherited) > 0 {
		keys := map[string]string{}
		for _, f := range fields {
			keys[f.Name] = f.Name
		}
		for _, f := range fields {
			key := ReadKey(f.Name, f.Def.Kind.String())
			if key == "" || key == f.Name {
				continue
			}
			if prior, exists := keys[key]; exists {
				return nil, fmt.Errorf("expanded read key %q from %s conflicts with composed field %s", key, f.Name, prior)
			}
			keys[key] = f.Name
		}
	}
	return fields, nil
}
