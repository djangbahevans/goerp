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

func TestQueryOptions_WithTxSetsTxID(t *testing.T) {
	tx := &Tx{id: "tx-abc"}
	var in dbQueryInput
	WithTx(tx)(&in)
	if in.TxID != "tx-abc" {
		t.Errorf("TxID = %q, want tx-abc", in.TxID)
	}
}

func TestQueryOptions_WithReadOnlySetsField(t *testing.T) {
	var in dbQueryInput
	WithReadOnly()(&in)
	if !in.Opts.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
}
