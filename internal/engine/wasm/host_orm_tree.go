package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"uuid"

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
// goerp#340) already uses for Sequence fields. A self-referencing label
// needs the row's own primary key before the row exists (no follow-up
// UPDATE) — the primary key itself is Readonly (goerp#992) and normally
// left to Postgres's own DEFAULT, but that default only resolves at
// INSERT time, too late for this function's own needs, so a model that
// declares any .Tree() field gets its primary key generated here in Go
// instead (the same uuid.NewV7 rotation already used for etag) whenever
// the caller omitted it — genPK is that value when generated, "" when
// record already had its own (this function has no legitimate reason to
// ever see a client-populated pkCol under #992, but doesn't assume so
// either), for the caller to fold into its own serverFilled list. If a
// declared parent doesn't exist, this leaves the path unset and lets
// the Many2One field's own FK constraint (Tree is just a modifier on
// Many2One) surface the real error at INSERT time, rather than
// duplicating that check here.
func injectTreePathOnCreate(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, record map[string]any) (genPK string, hostErr *abi.HostError) {
	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return "", nil
	}
	hasTreeField := false
	for _, f := range md.Fields {
		if f.Def.IsTree {
			hasTreeField = true
			break
		}
	}
	if !hasTreeField {
		return "", nil
	}
	ownPK, ok := record[pkCol]
	if !ok {
		genPK = uuid.NewV7().String()
		record[pkCol] = genPK
		ownPK = genPK
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

		parentPath, lookupErr := lookupTreePath(ctx, tx, md, f.Name, parentID)
		if lookupErr != nil {
			return genPK, lookupErr
		}
		if parentPath == "" {
			// No such parent row — leave the path unset; the ordinary
			// Many2One FK constraint rejects the INSERT with a clearer,
			// standard foreign_key_violation.
			continue
		}
		record[f.Name+"_path"] = parentPath + "." + ownLabel
	}
	return genPK, nil
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
				return ormSQLError(err)
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
			return ormSQLError(err)
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
		return "", ormSQLError(err)
	}
	return path.String, nil
}
