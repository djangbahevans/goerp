package db

import (
	"testing"
)

func TestToSnakeCase(t *testing.T) {
	cases := map[string]string{
		"ID":           "id",
		"Name":         "name",
		"CustomerName": "customer_name",
		"CompanyID":    "company_id",
		"CreatedAt":    "created_at",
		"HTTPStatus":   "http_status",
	}
	for in, want := range cases {
		if got := toSnakeCase(in); got != want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

type reflectTestRecord struct {
	ID        string `db:"id"`
	Name      string
	CompanyID *string
	Secret    string `db:"-"`
	unexp     string //nolint:unused
}

func TestStructColumnsAndValues(t *testing.T) {
	company := "acme"
	rec := reflectTestRecord{ID: "1", Name: "Widget", CompanyID: &company, Secret: "shh"}

	cols, vals, err := structColumnsAndValues(rec)
	if err != nil {
		t.Fatalf("structColumnsAndValues: %v", err)
	}
	wantCols := []string{"id", "name", "company_id"}
	if len(cols) != len(wantCols) {
		t.Fatalf("cols = %v, want %v", cols, wantCols)
	}
	for i, c := range wantCols {
		if cols[i] != c {
			t.Errorf("cols[%d] = %q, want %q", i, cols[i], c)
		}
	}
	if vals[0] != "1" || vals[1] != "Widget" {
		t.Errorf("vals = %v", vals)
	}
	if got, ok := vals[2].(*string); !ok || *got != "acme" {
		t.Errorf("vals[2] = %v, want *string(acme)", vals[2])
	}
}

func TestStructColumnsAndValues_Pointer(t *testing.T) {
	rec := &reflectTestRecord{ID: "2", Name: "Gadget"}
	cols, _, err := structColumnsAndValues(rec)
	if err != nil {
		t.Fatalf("structColumnsAndValues: %v", err)
	}
	if len(cols) != 3 {
		t.Fatalf("cols = %v, want 3 columns", cols)
	}
}

func TestStructColumnsAndValues_NilPointer(t *testing.T) {
	var rec *reflectTestRecord
	if _, _, err := structColumnsAndValues(rec); err == nil {
		t.Fatal("expected an error for a nil record pointer")
	}
}

// TestStructColumnsAndValues_NilAny is a regression test: reflect.ValueOf
// on a bare untyped nil (Insert("table", nil)) returns the zero Value,
// whose Kind() is reflect.Invalid — not reflect.Pointer — so the
// nil-pointer guard above never fires, and calling .Type() on it panics.
func TestStructColumnsAndValues_NilAny(t *testing.T) {
	if _, _, err := structColumnsAndValues(nil); err == nil {
		t.Fatal("expected an error for a nil record")
	}
}

type scanRowTestModel struct {
	ID        string `db:"id"`
	Name      string `db:"name"`
	CompanyID *string
	Count     int64
}

func TestScanRow_PopulatesFieldsPositionally(t *testing.T) {
	company := "acme"
	got, err := scanRow[scanRowTestModel]([]string{"id", "name", "company_id", "count"}, []any{"1", "Widget", company, int64(5)})
	if err != nil {
		t.Fatalf("scanRow: %v", err)
	}
	if got.ID != "1" || got.Name != "Widget" {
		t.Errorf("got = %+v", got)
	}
	if got.CompanyID == nil || *got.CompanyID != "acme" {
		t.Errorf("CompanyID = %v, want acme", got.CompanyID)
	}
	if got.Count != 5 {
		t.Errorf("Count = %d, want 5", got.Count)
	}
}

func TestScanRows_PopulatesEachRow(t *testing.T) {
	cols := []string{"id", "name", "company_id", "count"}
	got, err := scanRows[scanRowTestModel](cols, [][]any{
		{"1", "Widget", "acme", int64(5)},
		{"2", "Gadget", nil, int64(0)},
	})
	if err != nil {
		t.Fatalf("scanRows: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("scanRows returned %d rows, want 2", len(got))
	}
	if got[0].ID != "1" || got[0].Name != "Widget" || got[0].CompanyID == nil || *got[0].CompanyID != "acme" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].ID != "2" || got[1].CompanyID != nil {
		t.Errorf("got[1] = %+v", got[1])
	}
}

func TestScanRows_EmptyRowsReturnsEmptySlice(t *testing.T) {
	got, err := scanRows[scanRowTestModel]([]string{"id"}, nil)
	if err != nil {
		t.Fatalf("scanRows: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("scanRows = %+v, want empty", got)
	}
}

func TestScanRow_NilValueLeavesPointerNil(t *testing.T) {
	got, err := scanRow[scanRowTestModel]([]string{"id", "name", "company_id", "count"}, []any{"1", "Widget", nil, int64(0)})
	if err != nil {
		t.Fatalf("scanRow: %v", err)
	}
	if got.CompanyID != nil {
		t.Errorf("CompanyID = %v, want nil", got.CompanyID)
	}
}

func TestScanRow_NumericWidening(t *testing.T) {
	// msgpack-decoded integers commonly arrive as int64/float64 —
	// scanRow must convert into whatever numeric type the struct field
	// itself declares, not just the exact wire type.
	type row struct {
		Count int `db:"count"`
	}
	got, err := scanRow[row]([]string{"count"}, []any{int64(42)})
	if err != nil {
		t.Fatalf("scanRow: %v", err)
	}
	if got.Count != 42 {
		t.Errorf("Count = %d, want 42", got.Count)
	}
}

// TestScanRow_NullIntoNonPointerField_Errors matches database/sql.Scan's
// own precedent (e.g. "converting NULL to string is unsupported") — a
// non-pointer field can't represent NULL, so it must error rather than
// silently zero-value, which would make a real NULL indistinguishable
// from an actual empty/zero value.
func TestScanRow_NullIntoNonPointerField_Errors(t *testing.T) {
	type row struct {
		Name string `db:"name"`
	}
	if _, err := scanRow[row]([]string{"name"}, []any{nil}); err == nil {
		t.Fatal("expected an error assigning NULL into a non-pointer field")
	}
}

func TestScanRow_UnknownColumnIsSkipped(t *testing.T) {
	got, err := scanRow[scanRowTestModel]([]string{"id", "unknown_column"}, []any{"1", "whatever"})
	if err != nil {
		t.Fatalf("scanRow: %v", err)
	}
	if got.ID != "1" {
		t.Errorf("ID = %q, want 1", got.ID)
	}
}
