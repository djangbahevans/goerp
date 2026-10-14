package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/modelmeta"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

const displayNameField = "display_name"

// Related lookups share the primary query's tenant transaction and RLS.
// Missing or inaccessible targets expand to null while preserving the FK.
func expandRelations(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, md model.ModelDeclaration, columns []string, records []map[string]any, maskRelated bool) error {
	for _, colName := range columns {
		f, ok := fieldByName(md, colName)
		if ok && f.Def.Kind == model.KindMany2Many {
			if err := expandMany2Many(ctx, tx, modCtx, f, records, maskRelated); err != nil {
				return fmt.Errorf("expand relation %s: %w", f.Name, err)
			}
			continue
		}
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
	readKey := modelmeta.ReadKey(f.Name, f.Def.Kind.String())

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

	where := activeModelWhere(targetMD, fmt.Sprintf("%s IN (%s)", quoteIdentORM(targetPK), strings.Join(placeholders, ", ")))

	sqlStr := fmt.Sprintf("SELECT %s FROM %s WHERE %s",
		joinQuoted(selectCols), quoteIdentORM(modeltable.Name(targetMD)), where)
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
			// Missing, deleted or RLS-hidden targets leave the FK intact.
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
