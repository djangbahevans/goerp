package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// displayNameField is the literal field name expandRelations falls back
// to for a related record's label, per the label-field precedence chain
// documented in view-system.md/host-abi-reference.md §5a's Many2One
// read-shape ("{field_id, field: {id, display_name}}"). The chain's
// stronger rungs (a declared list view's `primary: true` column, the
// model's own `.Primary()` field) aren't available yet — `.Primary()`
// doesn't exist in the SDK (goerp#352) — so this is the only rung host.orm
// can consult today. A target model with no field literally named
// "display_name" still expands, just without a display_name key.
const displayNameField = "display_name"

// expandRelations resolves every Many2One column present in columns into
// its documented read shape: {field}_id (already present, unchanged) plus
// an expanded {field} object ({id, display_name}) added in place — the
// _id-suffix-stripped read key convention from go-sdk-reference.md §22
// "Many2One". Runs inside the same tenant-scoped transaction the primary
// query used, so the same RLS policies apply to the related lookups too:
// a related row the caller's RLS policy excludes resolves to a null
// {field}, never an error (multitenancy-internals.md "Fail-closed, not
// fail-open").
//
// A relation's target may belong to any loaded module; a target whose
// module isn't loaded (an absent soft dependency) leaves {field}_id in
// place and sets {field} to null. When maskRelated is true, the target
// model's own read rules apply to each expanded object.
func expandRelations(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, md model.ModelDeclaration, columns []string, records []map[string]any, maskRelated bool) error {
	for _, colName := range columns {
		f, ok := fieldByName(md, colName)
		if !ok || f.Def.Kind != model.KindMany2One {
			continue
		}
		if err := expandOneRelation(ctx, tx, modCtx, f, records, maskRelated); err != nil {
			return fmt.Errorf("expand relation %s: %w", f.Name, err)
		}
	}
	return nil
}

func expandOneRelation(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, f model.NamedField, records []map[string]any, maskRelated bool) error {
	readKey := strings.TrimSuffix(f.Name, "_id")

	targetMD, targetLoaded, ok := resolveRelationTarget(modCtx, f.Def.RelatedModel)
	if !targetLoaded {
		nullRelation(records, f.Name, readKey)
		return nil
	}
	if !ok {
		return fmt.Errorf("related_model %q not found", f.Def.RelatedModel)
	}
	targetPK, ok := primaryKeyColumn(targetMD)
	if !ok {
		return fmt.Errorf("related_model %q declares no primary key field", f.Def.RelatedModel)
	}
	hasDisplayName := false
	for _, tf := range targetMD.Fields {
		if tf.Name == displayNameField {
			hasDisplayName = true
			break
		}
	}

	fkValues := distinctNonNilStrings(records, f.Name)
	if len(fkValues) == 0 {
		nullRelation(records, f.Name, readKey)
		return nil
	}

	selectCols := []string{targetPK}
	if hasDisplayName {
		selectCols = append(selectCols, displayNameField)
	}
	placeholders := make([]string, len(fkValues))
	args := make([]any, len(fkValues))
	for i, v := range fkValues {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = v
	}

	sqlStr := fmt.Sprintf("SELECT %s FROM %s WHERE %s IN (%s)",
		joinQuoted(selectCols), quoteIdentORM(modeltable.Name(targetMD)), quoteIdentORM(targetPK), strings.Join(placeholders, ", "))
	rows, err := tx.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	related, err := scanRowsToMaps(rows)
	if err != nil {
		return err
	}

	pkValues := make([]any, len(related))
	for i, r := range related {
		pkValues[i] = r[targetPK]
	}
	if maskRelated {
		applyFieldMasking(modCtx, f.Def.RelatedModel, related)
		for i, r := range related {
			r[targetPK] = pkValues[i]
		}
	}
	byPK := make(map[string]map[string]any, len(related))
	for i, r := range related {
		byPK[fmt.Sprintf("%v", pkValues[i])] = r
	}

	for _, record := range records {
		fkVal, ok := record[f.Name]
		if !ok {
			continue
		}
		if fkVal == nil {
			record[readKey] = nil
			continue
		}
		if rel, found := byPK[fmt.Sprintf("%v", fkVal)]; found {
			record[readKey] = rel
		} else {
			// Missing/RLS-excluded target row — fail closed, not an error.
			record[readKey] = nil
		}
	}

	return nil
}

// resolveRelationTarget resolves a Many2One's related model against the
// calling module's own models first, then every loaded module's.
// targetLoaded is false only when the model's module is positively absent
// from the loaded modules; ok is false when the module is loaded (or
// can't be checked: a malformed name, or a request with no registry
// snapshot) but declares no such model.
func resolveRelationTarget(modCtx *ModuleContext, qualifiedName string) (md model.ModelDeclaration, targetLoaded, ok bool) {
	if md, ok := resolveModel(modCtx, qualifiedName); ok {
		return md, true, true
	}
	moduleName, _, hasModule := strings.Cut(qualifiedName, ".")
	targets := modCtx.ComputeTargets()
	if !hasModule || targets == nil {
		return model.ModelDeclaration{}, true, false
	}
	if _, loaded := targets[moduleName]; !loaded {
		return model.ModelDeclaration{}, false, false
	}
	md, ok = resolveAnyModel(modCtx, qualifiedName)
	return md, true, ok
}

func nullRelation(records []map[string]any, fkName, readKey string) {
	for _, record := range records {
		if _, ok := record[fkName]; ok {
			record[readKey] = nil
		}
	}
}

func fieldByName(md model.ModelDeclaration, name string) (model.NamedField, bool) {
	for _, f := range md.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return model.NamedField{}, false
}

func distinctNonNilStrings(records []map[string]any, key string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, record := range records {
		v, ok := record[key]
		if !ok || v == nil {
			continue
		}
		s := fmt.Sprintf("%v", v)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func joinQuoted(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = quoteIdentORM(n)
	}
	return strings.Join(quoted, ", ")
}
