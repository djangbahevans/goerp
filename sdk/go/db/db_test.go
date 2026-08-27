package db

import "testing"

func TestTx_RollbackAfterCommitIsNoop(t *testing.T) {
	// A committed Tx's Rollback must short-circuit before ever reaching
	// the host — this is exercisable without a real host.db import
	// (hostDBRollback panics outside wasip1) precisely because the
	// no-op path never calls it.
	tx := &Tx{id: "tx-1", committed: true}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() after Commit = %v, want nil", err)
	}
}

func TestTx_CommitAfterCommitIsNoop(t *testing.T) {
	tx := &Tx{id: "tx-1", committed: true}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() after Commit = %v, want nil", err)
	}
}

func TestTx_TxIDReturnsUnderlyingID(t *testing.T) {
	tx := &Tx{id: "tx-42"}
	if got := tx.TxID(); got != "tx-42" {
		t.Errorf("TxID() = %q, want %q", got, "tx-42")
	}
}

func TestBeginOptions_SetInputFields(t *testing.T) {
	var in dbBeginInput
	for _, opt := range []BeginOption{WithIsolation("serializable"), ReadOnly()} {
		opt(&in)
	}
	if in.Isolation != "serializable" {
		t.Errorf("Isolation = %q, want %q", in.Isolation, "serializable")
	}
	if !in.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
}
