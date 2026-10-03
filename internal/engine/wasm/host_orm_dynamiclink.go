package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// validateDynamicLinkPairs rejects a write that sets a DynamicLink field
// or its sibling reference-type field without the other — "dynamic link
// fields must be set together" (go-sdk-reference.md §22). Pure/no DB
// access, so it runs alongside validateRequired, before a transaction is
// even opened.
func validateDynamicLinkPairs(md model.ModelDeclaration, record map[string]any) *abiv1.HostError {
	for _, f := range md.Fields {
		if f.Def.Kind != model.KindDynamicLink {
			continue
		}
		_, hasID := record[f.Name]
		_, hasType := record[f.Def.ReferenceTypeField]
		if hasID != hasType {
			return &abiv1.HostError{
				Code:    abiv1.ErrCodeValidationFailed,
				Message: "dynamic link fields " + f.Def.ReferenceTypeField + " and " + f.Name + " must be set together",
				Details: map[string]any{"field": f.Name},
			}
		}
	}
	return nil
}

// DynamicLink targets may belong to any loaded module and have no SQL FK.
func checkDynamicLinkTargets(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, md model.ModelDeclaration, record map[string]any) *abiv1.HostError {
	for _, f := range md.Fields {
		if f.Def.Kind != model.KindDynamicLink {
			continue
		}
		idVal, hasID := record[f.Name]
		typeVal, hasType := record[f.Def.ReferenceTypeField]
		if !hasID || !hasType {
			continue
		}
		typeName, ok := typeVal.(string)
		if !ok {
			return &abiv1.HostError{Code: abiv1.ErrCodeDynamicLinkTargetNotFound, Message: f.Def.ReferenceTypeField + " must be a string naming a model", Details: map[string]any{"field": f.Name}}
		}

		targetMD, ok := resolveAnyModel(modCtx, typeName)
		if !ok {
			return &abiv1.HostError{Code: abiv1.ErrCodeDynamicLinkTargetNotFound, Message: "model " + typeName + " is not a known model", Details: map[string]any{"field": f.Name}}
		}
		targetPK, ok := primaryKeyColumn(targetMD)
		if !ok {
			return &abiv1.HostError{Code: abiv1.ErrCodeDynamicLinkTargetNotFound, Message: "model " + typeName + " declares no primary key field", Details: map[string]any{"field": f.Name}}
		}

		table := quoteIdentORM(modeltable.Name(targetMD))
		var exists bool
		sqlStr := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE %s)", table, activeModelWhere(targetMD, quoteIdentORM(targetPK)+" = $1"))
		if err := tx.QueryRowContext(ctx, sqlStr, idVal).Scan(&exists); err != nil {
			return ormSQLError(err)
		}
		if !exists {
			return &abiv1.HostError{Code: abiv1.ErrCodeDynamicLinkTargetNotFound, Message: f.Name + " does not exist in model " + typeName, Details: map[string]any{"field": f.Name}}
		}
	}
	return nil
}

// resolveAnyModel resolves a fully qualified "{module}.{resource}" model
// name against every loaded module's own declared models
// (modCtx.ComputeTargets()) — unlike resolveModel (host_orm.go), which
// only ever resolves the calling module's own models, this is used for
// DynamicLink target verification, where the referenced model can belong
// to any loaded module.
func resolveAnyModel(modCtx *ModuleContext, qualifiedName string) (model.ModelDeclaration, bool) {
	moduleName, _, found := strings.Cut(qualifiedName, ".")
	if !found {
		return model.ModelDeclaration{}, false
	}
	target, ok := modCtx.ComputeTargets()[moduleName]
	if !ok {
		return model.ModelDeclaration{}, false
	}
	for _, md := range target.ModelDecls {
		if md.QualifiedName(moduleName) == qualifiedName {
			return md, true
		}
	}
	return model.ModelDeclaration{}, false
}
