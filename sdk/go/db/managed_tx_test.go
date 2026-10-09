package db

import (
	"errors"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestNewManagedTx_RequiresTransactionID(t *testing.T) {
	tx, err := NewManagedTx("")
	if err == nil || tx != nil {
		t.Fatalf("NewManagedTx with no ID = %v, %v, want nil and an error", tx, err)
	}
}

func TestManagedTx_RejectsCommitAndRollbackWithoutCallingHost(t *testing.T) {
	tx, err := NewManagedTx("delivery-tx")
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		for name, operation := range map[string]func() error{
			"Commit":   tx.Commit,
			"Rollback": tx.Rollback,
		} {
			err := operation()
			hostErr, ok := errors.AsType[*abi.HostError](err)
			if !ok || hostErr.Code != abi.ErrCodeTransactionManaged {
				t.Fatalf("%s = %v, want db.transaction_managed", name, err)
			}
		}
	}

	if tx.TxID() != "delivery-tx" || tx.committed {
		t.Fatalf("rejected operations changed the handle: %+v", tx)
	}
}
