package orm

import "testing"

func TestQuery_Where_CombinesWithAnd(t *testing.T) {
	name := NewField[valuesTestModel, string]("name")

	q := From[valuesTestModel]().Where(name.Eq("a")).Where(name.Eq("b"))
	want := "(record.name = 'a') AND (record.name = 'b')"
	if got := q.domain(); got != want {
		t.Errorf("domain() = %q, want %q", got, want)
	}
}

func TestQuery_Where_FirstCallDoesNotWrapAlone(t *testing.T) {
	name := NewField[valuesTestModel, string]("name")

	q := From[valuesTestModel]().Where(name.Eq("a"))
	want := "record.name = 'a'"
	if got := q.domain(); got != want {
		t.Errorf("domain() = %q, want %q — a single Where must not wrap in AND()", got, want)
	}
}

func TestQuery_NoWhere_DomainIsEmpty(t *testing.T) {
	q := From[valuesTestModel]()
	if got := q.domain(); got != "" {
		t.Errorf("domain() = %q, want empty (host.orm.search's own always-match default)", got)
	}
}

func TestQuery_OrderBy_AppendsDescOnly(t *testing.T) {
	name := NewField[valuesTestModel, string]("name")

	q := From[valuesTestModel]().OrderBy(name, false)
	if q.order != "name" {
		t.Errorf("order = %q, want %q", q.order, "name")
	}

	q = From[valuesTestModel]().OrderBy(name, true)
	if q.order != "name DESC" {
		t.Errorf("order = %q, want %q", q.order, "name DESC")
	}
}

func TestQuery_Limit(t *testing.T) {
	q := From[valuesTestModel]().Limit(50)
	if q.limit != 50 {
		t.Errorf("limit = %d, want 50", q.limit)
	}
}

func TestQuery_Select_DefaultsToNilFields(t *testing.T) {
	q := From[valuesTestModel]()
	if q.fields != nil {
		t.Errorf("fields = %v, want nil when Select is never called (host.orm's own \"empty = all fields\" default applies)", q.fields)
	}
}

func TestQuery_Select_SetsFields(t *testing.T) {
	name := NewField[valuesTestModel, string]("name")
	q := From[valuesTestModel]().Select(name)
	if got := fieldNames(q.fields); len(got) != 1 || got[0] != "name" {
		t.Errorf("fields = %v, want [name]", got)
	}
}

// TestQuery_IDs_CursorRejected pins IDs' rejection of Cursor — checked
// before any host call, so this doesn't need a wasip1 build to exercise.
func TestQuery_IDs_CursorRejected(t *testing.T) {
	if _, err := From[valuesTestModel]().Cursor("abc123").IDs(); err == nil {
		t.Fatal("IDs() with a Cursor set: error = nil, want an error")
	}
}

// TestQuery_Count_CursorRejected mirrors TestQuery_IDs_CursorRejected.
func TestQuery_Count_CursorRejected(t *testing.T) {
	if _, err := From[valuesTestModel]().Cursor("abc123").Count(); err == nil {
		t.Fatal("Count() with a Cursor set: error = nil, want an error")
	}
}
