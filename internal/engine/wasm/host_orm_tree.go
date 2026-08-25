package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// This file holds .Tree() companion-path maintenance for host.orm's write
// half (goerp#379) — go-sdk-reference.md §22 "Tree". Engine-native, not a
// registered WASM hook: "purely structural maintenance derived from the
// parent chain, no business logic a module needs to supply" (the same
// reasoning the doc gives for model.Sequence being engine-native rather
// than hook-based).
//
// A row's label is its own primary key with hyphens stripped — ltree
// labels can't contain hyphens, and this codebase's UUID primary keys
// always do.

// ltreeLabel converts a caller-supplied primary key value into a valid
// ltree label.
func ltreeLabel(pkValue any) string {
	s := fmt.Sprint(pkValue)
	return strings.ReplaceAll(s, "-", "")
}

// injectTreePathOnCreate computes and injects the {field}_path value for
// every .Tree() field on md directly into record, before the INSERT —
// the same "inject a value the caller didn't supply, immediately before
// createOneRecordTx" shape acquireSequenceFields (host_orm_write.go,
// goerp#340) already uses for Sequence fields. Primary keys are
// caller-supplied in this codebase's convention, so a self-referencing
// label can be computed before the row exists — no follow-up UPDATE
// needed. If a declared parent doesn't exist, this leaves the path
// unset and lets the Many2One field's own FK constraint (Tree is just a
// modifier on Many2One) surface the real error at INSERT time, rather
// than duplicating that check here.
func injectTreePathOnCreate(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, record map[string]any) *abi.HostError {
	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return nil
	}
	ownPK, ok := record[pkCol]
	if !ok {
		return nil
	}
	ownLabel := ltreeLabel(ownPK)

	for _, f := range md.Fields {
		if !f.Def.IsTree {
			continue
		}
		parentID, hasParent := record[f.Name]
		if !hasParent || parentID == nil {
			record[f.Name+"_path"] = ownLabel
			continue
		}

		parentPath, hostErr := lookupTreePath(ctx, tx, md, f.Name, parentID)
		if hostErr != nil {
			return hostErr
		}
		if parentPath == "" {
			// No such parent row — leave the path unset; the ordinary
			// Many2One FK constraint rejects the INSERT with a clearer,
			// standard foreign_key_violation.
			continue
		}
		record[f.Name+"_path"] = parentPath + "." + ownLabel
	}
	return nil
}

// maintainTreePathOnWrite reparents a single row: cycle-checks the new
// parent against the record's own current path, then rewrites the moved
// row and every descendant's path in one UPDATE — go-sdk-reference.md
// §22's own formula. Only called when the tree field's own column key is
// present in the write diff; a write that doesn't touch it is a no-op.
func maintainTreePathOnWrite(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, pkCol, id string, record map[string]any) *abi.HostError {
	pkColQuoted := quoteIdentORM(pkCol)
	table := quoteIdentORM(tableNameForORM(md))

	for _, f := range md.Fields {
		if !f.Def.IsTree {
			continue
		}
		newParentID, touched := record[f.Name]
		if !touched {
			continue
		}

		oldPath, hostErr := lookupOwnTreePath(ctx, tx, table, pkColQuoted, f.Name, id)
		if hostErr != nil {
			return hostErr
		}
		if oldPath == "" {
			// No existing path recorded (e.g. this row predates the
			// field, or was never given a parent) — nothing to move.
			continue
		}

		var newPrefix string
		if newParentID == nil {
			newPrefix = ltreeLabel(id)
		} else {
			newParentPath, hostErr := lookupTreePath(ctx, tx, md, f.Name, newParentID)
			if hostErr != nil {
				return hostErr
			}
			if newParentPath == "" {
				continue // dangling parent — let the FK constraint reject it.
			}

			var wouldCycle bool
			if err := tx.QueryRowContext(ctx, "SELECT $1::ltree <@ $2::ltree", newParentPath, oldPath).Scan(&wouldCycle); err != nil {
				return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
			}
			if wouldCycle {
				return &abi.HostError{Code: abi.ErrCodeCycleDetected, Message: "reparenting " + id + " under " + fmt.Sprint(newParentID) + " would make it its own ancestor", Details: map[string]any{"field": f.Name}}
			}
			newPrefix = newParentPath + "." + ltreeLabel(id)
		}

		pathCol := quoteIdentORM(f.Name + "_path")
		updateSQL := fmt.Sprintf(
			"UPDATE %s SET %s = CASE WHEN %s = $1::ltree THEN $2::ltree ELSE $2::ltree || subpath(%s, nlevel($1::ltree)) END WHERE %s <@ $1::ltree",
			table, pathCol, pathCol, pathCol, pathCol,
		)
		if _, err := tx.ExecContext(ctx, updateSQL, oldPath, newPrefix); err != nil {
			return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
		}
	}
	return nil
}

// lookupTreePath returns treeField's "_path" companion column value for
// the row identified by pkValue on md's own table — "" if no such row
// exists.
func lookupTreePath(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, treeField string, pkValue any) (string, *abi.HostError) {
	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return "", nil
	}
	table := quoteIdentORM(tableNameForORM(md))
	return lookupOwnTreePath(ctx, tx, table, quoteIdentORM(pkCol), treeField, pkValue)
}

// lookupOwnTreePath is lookupTreePath's shared core, taking an
// already-quoted table/pk column pair so maintainTreePathOnWrite can
// reuse it for the record being written without re-deriving them.
func lookupOwnTreePath(ctx context.Context, tx *sql.Tx, table, pkColQuoted, treeField string, pkValue any) (string, *abi.HostError) {
	pathCol := quoteIdentORM(treeField + "_path")
	sqlStr := fmt.Sprintf("SELECT %s FROM %s WHERE %s = $1", pathCol, table, pkColQuoted)
	var path sql.NullString
	err := tx.QueryRowContext(ctx, sqlStr, pkValue).Scan(&path)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	return path.String, nil
}
