package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/modelmeta"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

const displayNameField = "display_name"

func checkRelationTargets(ctx context.Context, tx *sql.Tx, mc *ModuleContext, md model.ModelDeclaration, record map[string]any) *abiv1.HostError {
	if hostErr := checkFileReferences(ctx, tx, mc, md, record); hostErr != nil {
		return hostErr
	}

	return checkDynamicLinkTargets(ctx, tx, mc, md, record)
}

// Related lookups share the primary query's tenant transaction and RLS.
// Missing or inaccessible targets expand to null while preserving the FK.
func expandRelations(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, qualifiedModel string, md model.ModelDeclaration, columns []string, records []map[string]any, maskRelated bool) error {
	// Discriminators are lookup inputs even when their public values are masked.
	dynamicTargets := make(map[string][]string)
	for _, f := range md.Fields {
		if f.Def.Kind != model.KindDynamicLink || !slices.Contains(columns, f.Name) {
			continue
		}

		targets := make([]string, len(records))
		for i, record := range records {
			targets[i], _ = record[f.Def.ReferenceTypeField].(string)
		}
		dynamicTargets[f.Name] = targets
	}

	if maskRelated {
		applyFieldMasking(modCtx, qualifiedModel, records)
	}

	for _, colName := range columns {
		f, ok := fieldByName(md, colName)
		if !ok {
			continue
		}

		var err error
		switch f.Def.Kind {
		case model.KindFile:
			err = expandFileRelation(ctx, tx, modCtx, qualifiedModel, f, records, maskRelated)
		case model.KindMany2Many:
			err = expandMany2Many(ctx, tx, modCtx, f, records, maskRelated)
		case model.KindMany2One:
			err = expandOneRelation(ctx, tx, modCtx, f, records, maskRelated)
		case model.KindDynamicLink:
			err = expandDynamicRelation(ctx, tx, modCtx, qualifiedModel, f, records, dynamicTargets[f.Name], maskRelated)
		default:
			continue
		}
		if err != nil {
			return fmt.Errorf("expand relation %s: %w", f.Name, err)
		}
	}
	return nil
}

func relationQueryColumns(md model.ModelDeclaration, columns []string, pkCol string) []string {
	queryColumns := slices.Clone(columns)
	if !slices.Contains(queryColumns, pkCol) {
		queryColumns = append(queryColumns, pkCol)
	}

	for _, f := range md.Fields {
		if f.Def.Kind == model.KindDynamicLink && slices.Contains(columns, f.Name) && !slices.Contains(queryColumns, f.Def.ReferenceTypeField) {
			queryColumns = append(queryColumns, f.Def.ReferenceTypeField)
		}
	}
	return queryColumns
}

func removeInternalColumns(records []map[string]any, queryColumns, columns []string) {
	for _, col := range queryColumns {
		if slices.Contains(columns, col) {
			continue
		}
		for _, record := range records {
			delete(record, col)
		}
	}
}

func expandDynamicRelation(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, qualifiedModel string, f model.NamedField, records []map[string]any, targets []string, maskRelated bool) error {
	readKey := modelmeta.ReadKey(f.Name, f.Def.Kind.String())
	nullRelation(records, f.Name, readKey)
	if reg := modCtx.FieldSecRegistry(); maskRelated && reg != nil {
		if rule, ok := reg.Rule(qualifiedModel, f.Name); ok && !fieldReadAllowed(modCtx, modCtx.PermissionRegistry(), rule) {
			return nil
		}
	}

	byTarget := make(map[string][]map[string]any)
	for i, record := range records {
		if record[f.Name] != nil && targets[i] != "" {
			byTarget[targets[i]] = append(byTarget[targets[i]], record)
		}
	}

	for targetName, targetRecords := range byTarget {
		targetMD, _, ok := resolveRelationTarget(modCtx, targetName)
		if !ok || targetMD.Backend != "" {
			continue
		}
		if _, ok := primaryKeyColumn(targetMD); !ok {
			continue
		}

		f.Def.RelatedModel = targetName
		if err := expandOneRelation(ctx, tx, modCtx, f, targetRecords, maskRelated); err != nil {
			return err
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
	hasDisplayName := hasField(targetMD, displayNameField)

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
			record[readKey] = nil
		}
	}

	return nil
}

// targetLoaded distinguishes an absent soft dependency from a missing model
// in a loaded module; Many2One treats only the latter as a declaration error.
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
