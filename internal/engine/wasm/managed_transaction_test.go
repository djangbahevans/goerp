package wasm

import (
	"context"
	"errors"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
)

func TestManagedTransaction_Lifecycle(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			primary := openTestPrimaryDB(t)
			rt := newHostDBTestRuntime(t, primary, 1)
			mc := newTestModuleContext("managed_test", abi.CapDBWrite, rt.TxLimiter())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			started := time.Now()
			managed, err := rt.BeginManagedTransaction(ctx, mc)
			if err != nil {
				t.Fatal(err)
			}
			defer managed.Rollback()

			deadline, ok := managed.Context.Deadline()
			if !ok || deadline.Before(started.Add(29*time.Second)) || deadline.After(time.Now().Add(30*time.Second)) {
				t.Fatalf("managed transaction deadline = %v", deadline)
			}

			var userID, contactID, roles, searchPath string
			if err := managed.Tx.QueryRowContext(managed.Context, `SELECT current_setting('app.current_user_id'),
				current_setting('app.current_user_contact_id'), current_setting('app.current_user_roles'), current_setting('search_path')`).
				Scan(&userID, &contactID, &roles, &searchPath); err != nil {
				t.Fatal(err)
			}
			if userID != "user-1" || contactID != "contact-1" || roles != "admin" || searchPath != "tenant_managed_test, public" {
				t.Fatalf("incorrect tenant scope: user=%q contact=%q roles=%q search_path=%q", userID, contactID, roles, searchPath)
			}

			called := false
			if !mc.AfterCommit(managed.ID, func(ctx context.Context) {
				called = true
				if err := ctx.Err(); err != nil {
					t.Errorf("after-commit context already canceled: %v", err)
				}
			}) {
				t.Fatal("managed transaction did not accept an after-commit hook")
			}

			if _, err := rt.BeginManagedTransaction(ctx, mc); err == nil {
				t.Fatal("nested managed transaction was allowed")
			}
			if _, err := rt.BeginManagedTransaction(ctx, newTestModuleContext("other", abi.CapDBWrite, rt.TxLimiter())); err == nil {
				t.Fatal("managed transaction exceeded the limiter")
			} else if hostErr, ok := errors.AsType[*abiv1.HostError](err); !ok || hostErr.Code != abiv1.ErrCodeTransactionLimitExceeded {
				t.Fatalf("unexpected limiter error: %v", err)
			}

			switch outcome {
			case "commit":
				if err := managed.Commit(); err != nil {
					t.Fatal(err)
				}
			case "rollback":
				managed.Rollback()
			case "cancel":
				cancel()
				if err := managed.Commit(); err == nil {
					t.Fatal("canceled managed transaction committed")
				}
			}
			managed.Rollback()

			if called != (outcome == "commit") {
				t.Fatalf("after-commit hook called=%t for %s", called, outcome)
			}
			if mc.HasOpenTransaction() || primary.Stats().InUse != 0 {
				t.Fatal("managed transaction leaked its registration or pinned connection")
			}
			if !rt.TxLimiter().TryAcquire() {
				t.Fatal("managed transaction leaked its limiter slot")
			}
			rt.TxLimiter().Release()
		})
	}
}
