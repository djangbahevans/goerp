package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// UUID hyphens are invalid in ltree labels.
func ltreeLabel(pkValue any) string {
	s := fmt.Sprint(pkValue)
	return strings.ReplaceAll(s, "-", "")
}

// Tree paths include the new row's primary key, so it must be generated
// before INSERT can evaluate the database default.
func injectTreePathOnCreate(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, record map[string]any) (genPK string, hostErr *abiv1.HostError) {
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
			return genPK, &abiv1.HostError{Code: abiv1.ErrCodeForeignKeyViolation, Message: "tree parent not found", Details: map[string]any{"field": f.Name}}
		}
		record[f.Name+"_path"] = parentPath + "." + ownLabel
	}
	return genPK, nil
}

func maintainTreePathOnWrite(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, pkCol, id string, record map[string]any) *abiv1.HostError {
	pkColQuoted := quoteIdentORM(pkCol)
	table := quoteIdentORM(modeltable.Name(md))

	for _, f := range md.Fields {
		if !f.Def.IsTree {
			continue
		}
		newParentID, touched := record[f.Name]
		if !touched {
			continue
		}

		oldPath, hostErr := lookupOwnTreePath(ctx, tx, md, table, pkColQuoted, f.Name, id)
		if hostErr != nil {
			return hostErr
		}
		if oldPath == "" {
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
				return &abiv1.HostError{Code: abiv1.ErrCodeForeignKeyViolation, Message: "tree parent not found", Details: map[string]any{"field": f.Name}}
			}

			var wouldCycle bool
			if err := tx.QueryRowContext(ctx, "SELECT $1::ltree <@ $2::ltree", newParentPath, oldPath).Scan(&wouldCycle); err != nil {
				return ormSQLError(err)
			}
			if wouldCycle {
				return &abiv1.HostError{Code: abiv1.ErrCodeCycleDetected, Message: "reparenting " + id + " under " + fmt.Sprint(newParentID) + " would make it its own ancestor", Details: map[string]any{"field": f.Name}}
			}
			newPrefix = newParentPath + "." + ltreeLabel(id)
		}

		pathCol := quoteIdentORM(f.Name + "_path")
		updateSQL := fmt.Sprintf(
			"UPDATE %s SET %s = CASE WHEN %s = $1::ltree THEN $2::ltree ELSE $2::ltree || subpath(%s, nlevel($1::ltree)) END WHERE %s",
			table, pathCol, pathCol, pathCol, activeModelWhere(md, pathCol+" <@ $1::ltree"),
		)
		if _, err := tx.ExecContext(ctx, updateSQL, oldPath, newPrefix); err != nil {
			return ormSQLError(err)
		}
	}
	return nil
}

func lookupTreePath(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, treeField string, pkValue any) (string, *abiv1.HostError) {
	pkCol, ok := primaryKeyColumn(md)
	if !ok {
		return "", nil
	}
	table := quoteIdentORM(modeltable.Name(md))
	return lookupOwnTreePath(ctx, tx, md, table, quoteIdentORM(pkCol), treeField, pkValue)
}

func lookupOwnTreePath(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, table, pkColQuoted, treeField string, pkValue any) (string, *abiv1.HostError) {
	pathCol := quoteIdentORM(treeField + "_path")
	sqlStr := fmt.Sprintf("SELECT %s FROM %s WHERE %s", pathCol, table, activeModelWhere(md, pkColQuoted+" = $1"))
	var path sql.NullString
	err := tx.QueryRowContext(ctx, sqlStr, pkValue).Scan(&path)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", ormSQLError(err)
	}
	return path.String, nil
}
