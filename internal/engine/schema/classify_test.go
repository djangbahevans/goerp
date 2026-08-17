package schema

import (
	"testing"

	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
)

func strType(t string, size int) *schema.ColumnType {
	return &schema.ColumnType{Type: &schema.StringType{T: t, Size: size}}
}

func intType(t string) *schema.ColumnType {
	return &schema.ColumnType{Type: &schema.IntegerType{T: t}}
}

func TestClassifyChanges(t *testing.T) {
	e := &SchemaDiffEngine{}

	addTable := &schema.AddTable{T: schema.NewTable("t")}
	addIndex := &schema.AddIndex{I: schema.NewIndex("idx")}
	dropIndex := &schema.DropIndex{I: schema.NewIndex("idx")}
	nullableAddColumn := &schema.AddColumn{C: &schema.Column{Name: "c", Type: &schema.ColumnType{Null: true}}}
	requiredNoDefaultAddColumn := &schema.AddColumn{C: &schema.Column{Name: "c", Type: &schema.ColumnType{Null: false}}}
	requiredWithDefaultAddColumn := &schema.AddColumn{
		C: &schema.Column{Name: "c", Type: &schema.ColumnType{Null: false}, Default: &schema.RawExpr{X: "0"}},
	}
	dropColumn := &schema.DropColumn{C: &schema.Column{Name: "c"}}
	dropTable := &schema.DropTable{T: schema.NewTable("t")}
	renameColumn := &schema.RenameColumn{From: &schema.Column{Name: "a"}, To: &schema.Column{Name: "b"}}
	renameTable := &schema.RenameTable{From: schema.NewTable("a"), To: schema.NewTable("b")}
	addCheck := &schema.AddCheck{C: &schema.Check{Name: "t_c_check", Expr: "c IN ('a')"}}
	addForeignKey := &schema.AddForeignKey{F: &schema.ForeignKey{Symbol: "t_c_fkey"}}

	changes := []schema.Change{
		addTable, addIndex, dropIndex,
		nullableAddColumn, requiredNoDefaultAddColumn, requiredWithDefaultAddColumn,
		dropColumn, dropTable, renameColumn, renameTable,
		addCheck, addForeignKey,
	}

	safe, deferred, blocked := e.classifyChanges(changes)

	wantSafe := []schema.Change{addTable, addIndex, dropIndex, nullableAddColumn, requiredWithDefaultAddColumn}
	if len(safe) != len(wantSafe) {
		t.Fatalf("safe = %d changes, want %d: %v", len(safe), len(wantSafe), safe)
	}
	for i, c := range wantSafe {
		if safe[i].change != c {
			t.Errorf("safe[%d] = %#v, want %#v", i, safe[i].change, c)
		}
	}

	wantDeferred := []schema.Change{addCheck, addForeignKey}
	if len(deferred) != len(wantDeferred) {
		t.Fatalf("deferred = %d changes, want %d: %v", len(deferred), len(wantDeferred), deferred)
	}
	for i, c := range wantDeferred {
		if deferred[i].change != c {
			t.Errorf("deferred[%d] = %#v, want %#v", i, deferred[i].change, c)
		}
	}

	wantBlocked := []schema.Change{requiredNoDefaultAddColumn, dropColumn, dropTable, renameColumn, renameTable}
	if len(blocked) != len(wantBlocked) {
		t.Fatalf("blocked = %d changes, want %d: %v", len(blocked), len(wantBlocked), blocked)
	}
	for i, c := range wantBlocked {
		if blocked[i].change != c {
			t.Errorf("blocked[%d] = %#v, want %#v", i, blocked[i].change, c)
		}
	}
}

func TestIsSafeModifyColumn(t *testing.T) {
	tests := []struct {
		name string
		c    *schema.ModifyColumn
		want bool
	}{
		{
			"varchar widened to a bigger varchar",
			&schema.ModifyColumn{
				From:   &schema.Column{Type: strType(postgres.TypeVarChar, 20)},
				To:     &schema.Column{Type: strType(postgres.TypeVarChar, 40)},
				Change: schema.ChangeType,
			},
			true,
		},
		{
			"varchar narrowed",
			&schema.ModifyColumn{
				From:   &schema.Column{Type: strType(postgres.TypeVarChar, 40)},
				To:     &schema.Column{Type: strType(postgres.TypeVarChar, 20)},
				Change: schema.ChangeType,
			},
			false,
		},
		{
			"varchar widened to text",
			&schema.ModifyColumn{
				From:   &schema.Column{Type: strType(postgres.TypeVarChar, 20)},
				To:     &schema.Column{Type: strType(postgres.TypeText, 0)},
				Change: schema.ChangeType,
			},
			true,
		},
		{
			"integer widened to bigint",
			&schema.ModifyColumn{
				From:   &schema.Column{Type: intType(postgres.TypeInteger)},
				To:     &schema.Column{Type: intType(postgres.TypeBigInt)},
				Change: schema.ChangeType,
			},
			true,
		},
		{
			"bigint narrowed to integer",
			&schema.ModifyColumn{
				From:   &schema.Column{Type: intType(postgres.TypeBigInt)},
				To:     &schema.Column{Type: intType(postgres.TypeInteger)},
				Change: schema.ChangeType,
			},
			false,
		},
		{
			"nullable relaxed to NOT NULL",
			&schema.ModifyColumn{
				From:   &schema.Column{Type: &schema.ColumnType{Null: true}},
				To:     &schema.Column{Type: &schema.ColumnType{Null: false}},
				Change: schema.ChangeNull,
			},
			false,
		},
		{
			"NOT NULL relaxed to nullable",
			&schema.ModifyColumn{
				From:   &schema.Column{Type: &schema.ColumnType{Null: false}},
				To:     &schema.Column{Type: &schema.ColumnType{Null: true}},
				Change: schema.ChangeNull,
			},
			true,
		},
		{
			"default-only change",
			&schema.ModifyColumn{
				From:   &schema.Column{Type: &schema.ColumnType{}, Default: &schema.RawExpr{X: "1"}},
				To:     &schema.Column{Type: &schema.ColumnType{}, Default: &schema.RawExpr{X: "2"}},
				Change: schema.ChangeDefault,
			},
			true,
		},
		{
			"generated expression change",
			&schema.ModifyColumn{
				From:   &schema.Column{Type: &schema.ColumnType{}},
				To:     &schema.Column{Type: &schema.ColumnType{}},
				Change: schema.ChangeGenerated,
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSafeModifyColumn(tt.c); got != tt.want {
				t.Errorf("isSafeModifyColumn() = %v, want %v", got, tt.want)
			}
		})
	}
}
