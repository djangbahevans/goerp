package recoverycode

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/mfatest"
)

func TestVerify_TenantScopeAndReset(t *testing.T) {
	e := openTestEnv(t)
	userID := e.createUser(t)
	other := mfatest.NewTenant(t, e.conn)
	mfatest.AddMember(t, e.conn, other, userID)
	code := "AAAAA-BBBBB"
	cred := e.insertCode(t, userID, code)
	if _, err := e.conn.ExecContext(t.Context(), `UPDATE system.user_mfa SET tenant_id=$2 WHERE id=$1`, cred.ID, e.tenant.ID); err != nil {
		t.Fatal(err)
	}

	for _, scope := range []mfa.Scope{{TenantID: other.ID}, {TenantID: e.tenant.ID, PlatformOnly: true}} {
		valid, _, err := e.service.Verify(t.Context(), userID, code, scope)
		if err != nil || valid {
			t.Fatalf("scope %v accepted tenant code: %v, %v", scope, valid, err)
		}
	}

	if err := e.service.store.WithTx(t.Context(), func(tx *sql.Tx) error {
		return e.service.store.SetResetTx(t.Context(), tx, userID, e.tenant.ID, true)
	}); err != nil {
		t.Fatal(err)
	}

	valid, _, err := e.service.Verify(t.Context(), userID, code, mfa.Scope{TenantID: e.tenant.ID})
	if err != nil || valid {
		t.Fatalf("reset tenant accepted code: %v, %v", valid, err)
	}

	if err := e.service.store.WithTx(t.Context(), func(tx *sql.Tx) error {
		return e.service.store.SetResetTx(t.Context(), tx, userID, e.tenant.ID, false)
	}); err != nil {
		t.Fatal(err)
	}

	valid, id, err := e.service.Verify(t.Context(), userID, code, mfa.Scope{TenantID: e.tenant.ID})
	if err != nil || !valid || id != cred.ID {
		t.Fatalf("own tenant code = %v, %s, %v", valid, id, err)
	}
}

func TestVerifyTx_RollbackPreservesCode(t *testing.T) {
	e := openTestEnv(t)
	userID := e.createUser(t)
	code := "AAAAA-BBBBB"
	e.insertCode(t, userID, code)
	writeFailure := errors.New("session write failed")

	err := e.service.store.WithTx(t.Context(), func(tx *sql.Tx) error {
		valid, _, err := e.service.VerifyTx(t.Context(), tx, userID, code, mfa.Scope{TenantID: e.tenant.ID})
		if err != nil || !valid {
			t.Fatalf("transaction verification = %v, %v", valid, err)
		}

		return writeFailure
	})
	if !errors.Is(err, writeFailure) {
		t.Fatalf("rollback error = %v", err)
	}

	valid, _, err := e.service.Verify(t.Context(), userID, code, mfa.Scope{TenantID: e.tenant.ID})
	if err != nil || !valid {
		t.Fatalf("code was spent by rolled-back transaction: %v, %v", valid, err)
	}
}
