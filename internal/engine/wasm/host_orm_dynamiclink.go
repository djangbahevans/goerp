package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// This file holds DynamicLink field validation for host.orm's write half
// (goerp#379) — go-sdk-reference.md §22 "DynamicLink". A DynamicLink
// field has no FK (its target table varies per row, and a Postgres FK
// can only ever reference one table), so the engine itself enforces what
// Postgres can't: both reference_type/reference_id present together, and
// reference_id actually existing in whichever model reference_type
// names.

// validateDynamicLinkPairs rejects a write that sets a DynamicLink field
// or its sibling reference-type field without the other — "dynamic link
// fields must be set together" (go-sdk-reference.md §22). Pure/no DB
// access, so it runs alongside validateRequired, before a transaction is
// even opened.
func validateDynamicLinkPairs(md model.ModelDeclaration, record map[string]any) *abi.HostError {
	for _, f := range md.Fields {
		if f.Def.Kind != model.KindDynamicLink {
			continue
		}
		_, hasID := record[f.Name]
		_, hasType := record[f.Def.ReferenceTypeField]
		if hasID != hasType {
			return &abi.HostError{
				Code:    abi.ErrCodeValidationFailed,
				Message: "dynamic link fields " + f.Def.ReferenceTypeField + " and " + f.Name + " must be set together",
				Details: map[string]any{"field": f.Name},
			}
		}
	}
	return nil
}

// checkDynamicLinkTargets verifies, for every DynamicLink field present in
// record, that record[ReferenceTypeField] names a known model and
// record[field] exists as a row in that model's table. The target model
// can belong to a different module than the one calling host.orm — a
// Comment/Attachment model's reference_type allowlist can span modules —
// resolved via resolveAnyModel (modCtx.ComputeTargets(), the same
// cross-module lookup goerp#377 already established for the Many2One-hop
// computed-field case). Runs inside tx so a rejection aborts the same
// transaction as any other write validation failure.
func checkDynamicLinkTargets(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, md model.ModelDeclaration, record map[string]any) *abi.HostError {
	for _, f := range md.Fields {
		if f.Def.Kind != model.KindDynamicLink {
			continue
		}
		idVal, hasID := record[f.Name]
		typeVal, hasType := record[f.Def.ReferenceTypeField]
		if !hasID || !hasType {
			continue // validateDynamicLinkPairs already rejects a lone one.
		}
		typeName, ok := typeVal.(string)
		if !ok {
			return &abi.HostError{Code: abi.ErrCodeDynamicLinkTargetNotFound, Message: f.Def.ReferenceTypeField + " must be a string naming a model", Details: map[string]any{"field": f.Name}}
		}

		targetMD, ok := resolveAnyModel(modCtx, typeName)
		if !ok {
			return &abi.HostError{Code: abi.ErrCodeDynamicLinkTargetNotFound, Message: "model " + typeName + " is not a known model", Details: map[string]any{"field": f.Name}}
		}
		targetPK, ok := primaryKeyColumn(targetMD)
		if !ok {
			return &abi.HostError{Code: abi.ErrCodeDynamicLinkTargetNotFound, Message: "model " + typeName + " declares no primary key field", Details: map[string]any{"field": f.Name}}
		}

		table := quoteIdentORM(tableNameForORM(targetMD))
		var exists bool
		sqlStr := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE %s = $1)", table, quoteIdentORM(targetPK))
		if err := tx.QueryRowContext(ctx, sqlStr, idVal).Scan(&exists); err != nil {
			return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
		}
		if !exists {
			return &abi.HostError{Code: abi.ErrCodeDynamicLinkTargetNotFound, Message: f.Name + " does not exist in model " + typeName, Details: map[string]any{"field": f.Name}}
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
	moduleName, resource, found := strings.Cut(qualifiedName, ".")
	if !found {
		return model.ModelDeclaration{}, false
	}
	target, ok := modCtx.ComputeTargets()[moduleName]
	if !ok {
		return model.ModelDeclaration{}, false
	}
	for _, md := range target.ModelDecls {
		if md.Name == resource {
			return md, true
		}
	}
	return model.ModelDeclaration{}, false
}
