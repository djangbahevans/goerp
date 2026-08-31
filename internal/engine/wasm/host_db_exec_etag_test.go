package wasm

import (
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func etagMechanismWidgetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "widget",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "etag", Def: model.Text()},
			{Name: "name", Def: model.Text()},
		},
	}
}

func etagMechanismGadgetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "gadget",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text()},
		},
	}
}

func newEtagMechanismTestModuleContext() *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "00000000-0000-0000-0000-0000000000aa", "contact-1", []string{"admin"}, nil, "anytenant", "anytenant", "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls: []model.ModelDeclaration{etagMechanismWidgetModelDecl(), etagMechanismGadgetModelDecl()},
		})
}

func TestResolveEtagTable(t *testing.T) {
	mc := newEtagMechanismTestModuleContext()

	if _, hasEtag := resolveEtagTable(mc, "widget"); !hasEtag {
		t.Error("widget: expected hasEtag=true")
	}
	if _, hasEtag := resolveEtagTable(mc, "gadget"); hasEtag {
		t.Error("gadget: expected hasEtag=false (no etag column)")
	}
	if _, hasEtag := resolveEtagTable(mc, "nonexistent_table"); hasEtag {
		t.Error("nonexistent_table: expected hasEtag=false")
	}
}

func parsedUpdateStmt(t *testing.T, sqlText string) auditableExecStmt {
	t.Helper()
	tree, err := pg_query.Parse(sqlText)
	if err != nil {
		t.Fatalf("parse %q: %v", sqlText, err)
	}
	stmt, ok := parseAuditableExecStmt(tree)
	if !ok {
		t.Fatalf("parseAuditableExecStmt(%q): expected ok=true", sqlText)
	}
	return stmt
}

func TestWhereClauseHasEtagCheck(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want bool
	}{
		{"bare etag equality", "UPDATE widget SET name = $1 WHERE etag = $2", true},
		{"qualified etag equality against the target's own name", "UPDATE widget SET name = $1 WHERE widget.etag = $2", true},
		{"qualified etag equality against the target's own alias", "UPDATE widget AS w SET name = $1 WHERE w.etag = $2", true},
		{"etag on right-hand side", "UPDATE widget SET name = $1 WHERE $2 = etag", true},
		{"etag combined with other conditions via AND", "UPDATE widget SET name = $1 WHERE id = $2 AND etag = $3", true},
		{"etag AND'ed outside a sibling OR group", "UPDATE widget SET name = $1 WHERE etag = $2 AND (id = $3 OR id = $4)", true},
		{"no etag check at all", "UPDATE widget SET name = $1 WHERE id = $2", false},
		{"etag mentioned only as a value, not compared", "UPDATE widget SET name = $1 WHERE id = 'etag'", false},
		{"no where clause", "UPDATE widget SET name = $1", false},
		{"etag compared with inequality, not equality", "UPDATE widget SET name = $1 WHERE etag != $2", false},
		{"etag check inside an unrelated subquery is not this statement's own check", "UPDATE widget SET name = $1 WHERE id IN (SELECT order_id FROM line_items WHERE etag = $2)", false},
		{"etag check outside a subquery still detected alongside one", "UPDATE widget SET name = $1 WHERE etag = $2 AND id IN (SELECT order_id FROM line_items WHERE etag = $3)", true},
		{"etag check only reachable through OR is not a required condition", "UPDATE widget SET name = $1 WHERE id = $2 OR (etag = $3 AND name = $4)", false},
		{"etag check negated by NOT is not a required condition", "UPDATE widget SET name = $1 WHERE NOT (etag = $2)", false},
		{"etag qualified to a different, joined table is not the target's own check", "UPDATE widget SET name = other.name FROM other_table AS other WHERE widget.id = $1 AND other.etag = $2", false},
		{"etag qualified to the target amid an unrelated join is still detected", "UPDATE widget SET name = other.name FROM other_table AS other WHERE widget.id = $1 AND widget.etag = $2", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := parsedUpdateStmt(t, tt.sql)
			if got := whereClauseHasEtagCheck(stmt.WhereClause, stmt.Relation); got != tt.want {
				t.Errorf("whereClauseHasEtagCheck(%q) = %v, want %v", tt.sql, got, tt.want)
			}
		})
	}
}

func TestIsEtagMismatch(t *testing.T) {
	tests := []struct {
		name             string
		hasEtagCheck     bool
		rowsAffected     int64
		wantEtagMismatch bool
	}{
		{"etag checked, zero rows affected", true, 0, true},
		{"etag checked, one row affected", true, 1, false},
		{"no etag check, zero rows affected", false, 0, false},
		{"no etag check, one row affected", false, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEtagMismatch(tt.hasEtagCheck, tt.rowsAffected); got != tt.wantEtagMismatch {
				t.Errorf("isEtagMismatch(%v, %d) = %v, want %v", tt.hasEtagCheck, tt.rowsAffected, got, tt.wantEtagMismatch)
			}
		})
	}
}
