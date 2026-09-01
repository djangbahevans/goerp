package db

import "testing"

func TestQueryResult_AsMapsPairsColumnsWithValues(t *testing.T) {
	result := &QueryResult{
		Rows:        [][]any{{"id-1", "Alice"}, {"id-2", "Bob"}},
		ColumnNames: []string{"id", "name"},
	}

	got := result.AsMaps()

	if len(got) != 2 {
		t.Fatalf("AsMaps() returned %d rows, want 2", len(got))
	}
	if got[0]["id"] != "id-1" || got[0]["name"] != "Alice" {
		t.Errorf("got[0] = %+v, want id=id-1 name=Alice", got[0])
	}
	if got[1]["id"] != "id-2" || got[1]["name"] != "Bob" {
		t.Errorf("got[1] = %+v, want id=id-2 name=Bob", got[1])
	}
}

func TestQueryResult_AsMapsEmptyRowsReturnsEmptySlice(t *testing.T) {
	result := &QueryResult{ColumnNames: []string{"id"}}

	got := result.AsMaps()
	if len(got) != 0 {
		t.Errorf("AsMaps() = %+v, want empty", got)
	}
}

func TestQueryOptions_WithTimeoutSetsField(t *testing.T) {
	var in dbQueryInput
	WithTimeout(5000)(&in)
	if in.Opts.TimeoutMs != 5000 {
		t.Errorf("TimeoutMs = %d, want 5000", in.Opts.TimeoutMs)
	}
}

func TestQueryOptions_WithReadOnlySetsField(t *testing.T) {
	var in dbQueryInput
	WithReadOnly()(&in)
	if !in.Opts.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
}

type firstRowTestRecord struct {
	ID   string `db:"id"`
	Name string `db:"name"`
}

func TestFirstRow_EmptyRowsReturnsErrNotFound(t *testing.T) {
	res := &QueryResult{ColumnNames: []string{"id", "name"}}

	_, err := firstRow[firstRowTestRecord](res)
	if !IsNotFound(err) {
		t.Errorf("firstRow() error = %v, want ErrNotFound", err)
	}
}

func TestFirstRow_ScansOnlyTheFirstRow(t *testing.T) {
	res := &QueryResult{
		ColumnNames: []string{"id", "name"},
		Rows:        [][]any{{"id-1", "Alice"}, {"id-2", "Bob"}},
	}

	got, err := firstRow[firstRowTestRecord](res)
	if err != nil {
		t.Fatalf("firstRow: %v", err)
	}
	if got.ID != "id-1" || got.Name != "Alice" {
		t.Errorf("firstRow() = %+v, want id=id-1 name=Alice", got)
	}
}
