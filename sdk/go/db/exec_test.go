package db

import (
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
	"github.com/vmihailenco/msgpack/v5"
)

// TestDbExecInput_MsgpackWireShape marshals a real dbExecInput and
// decodes the raw bytes into a generic map, checking the actual field
// names host.db.exec sees on the wire — a struct-literal-only test
// can't catch a typo'd msgpack tag (e.g. "expect_row" instead of
// "expect_rows"), since Go field access doesn't go through the tag at
// all.
func TestDbExecInput_MsgpackWireShape(t *testing.T) {
	in := dbExecInput{SQL: "UPDATE widget SET name = $1", Params: []any{"x"}, TxID: "tx-1", Opts: dbExecOpts{Returning: "id,name", ExpectRows: true}}
	data, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var wire map[string]any
	if err := msgpack.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wire["sql"] != in.SQL {
		t.Errorf(`wire["sql"] = %v, want %q`, wire["sql"], in.SQL)
	}
	if wire["tx_id"] != in.TxID {
		t.Errorf(`wire["tx_id"] = %v, want %q`, wire["tx_id"], in.TxID)
	}
	opts, ok := wire["opts"].(map[string]any)
	if !ok {
		t.Fatalf(`wire["opts"] = %v, want a map`, wire["opts"])
	}
	if opts["returning"] != "id,name" {
		t.Errorf(`wire["opts"]["returning"] = %v, want "id,name"`, opts["returning"])
	}
	if opts["expect_rows"] != true {
		t.Errorf(`wire["opts"]["expect_rows"] = %v, want true`, opts["expect_rows"])
	}
}

func TestExecReturning_NoMappedFields_ReturnsError(t *testing.T) {
	type empty struct {
		unexported string //nolint:unused
	}
	if _, err := ExecReturning[empty]("INSERT INTO t DEFAULT VALUES"); err == nil {
		t.Fatal("expected an error for a type with no db-mapped fields")
	}
}

func TestTxExecReturning_NoMappedFields_ReturnsError(t *testing.T) {
	type empty struct {
		unexported string //nolint:unused
	}
	tx := &Tx{id: "tx-1"}
	if _, err := tx.ExecReturning[empty]("INSERT INTO t DEFAULT VALUES"); err == nil {
		t.Fatal("expected an error for a type with no db-mapped fields")
	}
}

// TestWrapExecError_NoRowsAffected_IsErrNotFound covers the sentinel
// wiring exec.go relies on for ExecReturning's own zero-match case.
func TestWrapExecError_NoRowsAffected_IsErrNotFound(t *testing.T) {
	raw := &hostcall.HostError{Code: "db.no_rows_affected", Message: "statement matched zero rows"}
	if got := wrapExecError(raw); !errors.Is(got, ErrNotFound) {
		t.Errorf("wrapExecError(no_rows_affected) = %v, want ErrNotFound", got)
	}
}
