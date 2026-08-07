package schema

import (
	"fmt"
	"strings"

	"ariga.io/atlas/sql/schema"
	"github.com/jackc/pgx/v5"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func concurrentIndexDDL(modelDecls []model.ModelDeclaration, c schema.Change) (string, error) {
	switch c := c.(type) {
	case *schema.DropIndex:
		return fmt.Sprintf("DROP INDEX CONCURRENTLY IF EXISTS %s", quoteIdent(c.I.Name)), nil
	case *schema.AddIndex:
		tbl, idx, ok := findDeclaredIndex(modelDecls, c.I.Name)
		if !ok {
			return "", fmt.Errorf("add-index change for %q has no matching declaration", c.I.Name)
		}
		return buildCreateIndexConcurrently(tbl, c.I.Name, idx), nil
	default:
		return "", fmt.Errorf("concurrentIndexDDL: unexpected change type %T", c)
	}
}

func findDeclaredIndex(modelDecls []model.ModelDeclaration, indexName string) (table string, idx model.IndexDef, ok bool) {
	for _, md := range modelDecls {
		for _, ni := range md.Indexes {
			if ni.Name == indexName {
				tbl := md.Table
				if tbl == "" {
					tbl = snakeCase(md.Name)
				}
				return tbl, ni.Def, true
			}
		}
	}
	return "", model.IndexDef{}, false
}

func buildCreateIndexConcurrently(table, name string, idx model.IndexDef) string {
	method := "btree"
	if idx.Kind == model.KindGIN {
		method = "gin"
	}

	unique := ""
	if idx.IsUnique {
		unique = "UNIQUE "
	}

	colList := strings.Join(idx.Columns, ", ")
	if idx.Kind == model.KindGIN && idx.Ops != "" && len(idx.Columns) == 1 {
		colList = fmt.Sprintf("%s %s", idx.Columns[0], idx.Ops)
	}

	stmt := fmt.Sprintf("CREATE %sINDEX CONCURRENTLY %s ON %s USING %s (%s)",
		unique, quoteIdent(name), quoteIdent(table), method, colList)
	if idx.WhereExpr != "" {
		stmt += " WHERE " + idx.WhereExpr
	}
	return stmt
}

func quoteIdent(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
