package db

import (
	"reflect"
	"strings"
	"testing"
)

func TestQueryBuilder_SQL_Basic(t *testing.T) {
	qb := NewQuery("sales_orders").
		Select("id", "name").
		Where("deleted_at IS NULL")

	want := "SELECT id, name FROM sales_orders WHERE deleted_at IS NULL"
	if got := qb.SQL(); got != want {
		t.Fatalf("SQL() = %q, want %q", got, want)
	}
}

func TestQueryBuilder_NoSelect_StarsAll(t *testing.T) {
	qb := NewQuery("sales_orders")
	want := "SELECT * FROM sales_orders"
	if got := qb.SQL(); got != want {
		t.Fatalf("SQL() = %q, want %q", got, want)
	}
}

func TestQueryBuilder_WhereIf_SkippedClauseRenumbers(t *testing.T) {
	qb := NewQuery("sales_orders").
		Where("deleted_at IS NULL").
		WhereIf(false, "state = $?", "cancelled").
		WhereIf(true, "customer_id = $?", "cust-1").
		WhereIf(true, "created_at >= $?", "2026-01-01")

	wantSQL := "SELECT * FROM sales_orders WHERE deleted_at IS NULL AND customer_id = $1 AND created_at >= $2"
	if got := qb.SQL(); got != wantSQL {
		t.Fatalf("SQL() = %q, want %q", got, wantSQL)
	}
	wantArgs := []any{"cust-1", "2026-01-01"}
	if got := qb.Args(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("Args() = %#v, want %#v", got, wantArgs)
	}
}

func TestQueryBuilder_WhereIf_MultiplePlaceholdersInOneClause(t *testing.T) {
	qb := NewQuery("sales_orders").
		WhereIf(true, "amount_total BETWEEN $? AND $?", 10, 100).
		WhereIf(true, "state = $?", "open")

	wantSQL := "SELECT * FROM sales_orders WHERE amount_total BETWEEN $1 AND $2 AND state = $3"
	if got := qb.SQL(); got != wantSQL {
		t.Fatalf("SQL() = %q, want %q", got, wantSQL)
	}
	wantArgs := []any{10, 100, "open"}
	if got := qb.Args(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("Args() = %#v, want %#v", got, wantArgs)
	}
}

func TestQueryBuilder_Cursor_AppendsTiebreakToOrderBy(t *testing.T) {
	qb := NewQuery("sales_orders").
		OrderBy("created_at DESC").
		Cursor("", "created_at", "id").
		Limit(20)

	want := "SELECT * FROM sales_orders ORDER BY created_at DESC, id"
	if got := qb.SQL(); got != want {
		t.Fatalf("SQL() = %q, want %q", got, want)
	}
}

func TestQueryBuilder_Cursor_NoDuplicateTiebreak(t *testing.T) {
	qb := NewQuery("sales_orders").
		OrderBy("id DESC").
		Cursor("", "id", "id")

	want := "SELECT * FROM sales_orders ORDER BY id DESC"
	if got := qb.SQL(); got != want {
		t.Fatalf("SQL() = %q, want %q", got, want)
	}
}

func TestQueryBuilder_Cursor_BeforeOrderBy_NoDuplicateTiebreak(t *testing.T) {
	qb := NewQuery("sales_orders").
		Cursor("", "id", "id").
		OrderBy("id, created_at DESC")

	want := "SELECT * FROM sales_orders ORDER BY id, created_at DESC"
	if got := qb.SQL(); got != want {
		t.Fatalf("SQL() = %q, want %q", got, want)
	}
}

func TestQueryBuilder_Limit_BakedWhenNotPaginated(t *testing.T) {
	qb := NewQuery("sales_orders").Limit(10)
	want := "SELECT * FROM sales_orders LIMIT 10"
	if got := qb.SQL(); got != want {
		t.Fatalf("SQL() = %q, want %q", got, want)
	}
}

func TestQueryBuilder_Limit_OmittedWhenPaginated(t *testing.T) {
	qb := NewQuery("sales_orders").
		Limit(10).
		Cursor("some-cursor", "created_at", "id")

	if got := qb.SQL(); strings.Contains(got, "LIMIT") {
		t.Fatalf("SQL() = %q, want no LIMIT clause when paginated via Cursor", got)
	}
}
