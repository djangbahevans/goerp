package db

import "testing"

type insertTestRecord struct {
	ID   string `db:"id"`
	Name string `db:"name"`
}

func TestBuildInsertSQL(t *testing.T) {
	sql, vals, err := buildInsertSQL("widget", insertTestRecord{ID: "1", Name: "Gadget"})
	if err != nil {
		t.Fatalf("buildInsertSQL: %v", err)
	}
	want := "INSERT INTO widget (id, name) VALUES ($1, $2)"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
	if len(vals) != 2 || vals[0] != "1" || vals[1] != "Gadget" {
		t.Errorf("vals = %v", vals)
	}
}

func TestBuildInsertSQL_InvalidTableName(t *testing.T) {
	if _, _, err := buildInsertSQL("widget; DROP TABLE widget --", insertTestRecord{ID: "1"}); err == nil {
		t.Fatal("expected an error for an invalid table identifier")
	}
}

func TestBuildInsertSQL_NoMappedFields(t *testing.T) {
	type empty struct {
		unexported string //nolint:unused
	}
	if _, _, err := buildInsertSQL("widget", empty{}); err == nil {
		t.Fatal("expected an error for a record with no db-mapped fields")
	}
}

func TestInsertReturning_NoMappedFields_ReturnsError(t *testing.T) {
	type empty struct {
		unexported string //nolint:unused
	}
	if _, err := InsertReturning[empty]("widget", insertTestRecord{ID: "1", Name: "Gadget"}); err == nil {
		t.Fatal("expected an error for a return type with no db-mapped fields")
	}
}
