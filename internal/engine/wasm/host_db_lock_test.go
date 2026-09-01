package wasm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/vmihailenco/msgpack/v5"
)

func unmarshalEnvelope(t *testing.T, env wireEnvelope, out any) error {
	t.Helper()
	return msgpack.Unmarshal(env.Data, out)
}

// beginLockTx calls call_begin on inst and returns the tx_id, failing the
// test on any error.
func beginLockTx(t *testing.T, ctx context.Context, inst *ModuleInstance) string {
	t.Helper()

	env := callHost(t, ctx, inst, "call_begin", dbBeginInput{})
	if !env.OK {
		t.Fatalf("begin failed: %+v", env.Error)
	}
	var out dbBeginOutput
	if err := unmarshalEnvelope(t, env, &out); err != nil {
		t.Fatalf("unmarshal begin output: %v", err)
	}
	return out.TxID
}

func TestHostDBLock_TryLock_AcquiresWhenFree(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dblocktrytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	txID := beginLockTx(t, ctx, inst)

	var out dbLockOutput
	env := callHost(t, ctx, inst, "call_lock", dbLockInput{Key: "widget-a", TxID: txID})
	if !env.OK {
		t.Fatalf("lock failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &out); err != nil {
		t.Fatalf("unmarshal lock output: %v", err)
	}
	if !out.Acquired {
		t.Error("Acquired = false, want true (lock is free)")
	}
}

func TestHostDBLock_TxIDNotFound(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dblocktxnotfoundtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_lock", dbLockInput{Key: "widget-a", TxID: "does-not-exist"})
	if env.OK {
		t.Fatal("expected an error for an unregistered tx_id")
	}
	if env.Error.Code != abi.ErrCodeTransactionNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeTransactionNotFound)
	}
}

func TestHostDBLock_CapabilityDenied(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dblockcaptest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter()) // no CapDBWrite
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_lock", dbLockInput{Key: "widget-a", TxID: "irrelevant"})
	if env.OK {
		t.Fatal("expected capability denial without db.write")
	}
	if env.Error.Code != abi.ErrCodeCapabilityDenied {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeCapabilityDenied)
	}
}

// A second transaction can't try-lock a key the first still holds, but
// can immediately afterward once the first commits.
func TestHostDBLock_TryLock_ContendsAcrossTransactions_ReleasesOnCommit(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dblockcontendtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc1 := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst1 := newHostDBQueryCaller(t, ctx, r, mc1)
	mc2 := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst2 := newHostDBQueryCaller(t, ctx, r, mc2)

	tx1 := beginLockTx(t, ctx, inst1)
	var out1 dbLockOutput
	env := callHost(t, ctx, inst1, "call_lock", dbLockInput{Key: "inventory:sku-1", TxID: tx1})
	if !env.OK {
		t.Fatalf("first lock failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &out1); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out1.Acquired {
		t.Fatal("first caller failed to acquire a free lock")
	}

	tx2 := beginLockTx(t, ctx, inst2)
	var out2 dbLockOutput
	env = callHost(t, ctx, inst2, "call_lock", dbLockInput{Key: "inventory:sku-1", TxID: tx2})
	if !env.OK {
		t.Fatalf("second lock call failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &out2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out2.Acquired {
		t.Error("second caller acquired a lock the first still holds")
	}

	if env := callHost(t, ctx, inst1, "call_commit", dbTxIDInput{TxID: tx1}); !env.OK {
		t.Fatalf("commit tx1 failed: %+v", env.Error)
	}

	env = callHost(t, ctx, inst2, "call_lock", dbLockInput{Key: "inventory:sku-1", TxID: tx2})
	if !env.OK {
		t.Fatalf("third lock call failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &out2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out2.Acquired {
		t.Error("second caller still couldn't acquire the lock after the first committed")
	}
}

func TestHostDBLock_TenantNamespacing_DifferentTenantsDontCollide(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slugA := fmt.Sprintf("dblocktenanta%d", time.Now().UnixNano())
	slugB := fmt.Sprintf("dblocktenantb%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slugA)
	createFixtureTenantSchema(t, primaryDB, slugB)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mcA := newTestModuleContext(slugA, abi.CapDBWrite, r.TxLimiter())
	instA := newHostDBQueryCaller(t, ctx, r, mcA)
	mcB := newTestModuleContext(slugB, abi.CapDBWrite, r.TxLimiter())
	instB := newHostDBQueryCaller(t, ctx, r, mcB)

	txA := beginLockTx(t, ctx, instA)
	var outA dbLockOutput
	env := callHost(t, ctx, instA, "call_lock", dbLockInput{Key: "same-key", TxID: txA})
	if !env.OK {
		t.Fatalf("tenant A lock call failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &outA); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !outA.Acquired {
		t.Fatal("tenant A failed to acquire its own free lock")
	}

	txB := beginLockTx(t, ctx, instB)
	var outB dbLockOutput
	env = callHost(t, ctx, instB, "call_lock", dbLockInput{Key: "same-key", TxID: txB})
	if !env.OK {
		t.Fatalf("tenant B lock call failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &outB); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !outB.Acquired {
		t.Error("tenant B couldn't acquire the same raw key tenant A holds — namespacing isn't isolating tenants")
	}
}

func TestHostDBLock_Shared_MultipleReadersCanHoldSimultaneously(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dblocksharedtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc1 := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst1 := newHostDBQueryCaller(t, ctx, r, mc1)
	mc2 := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst2 := newHostDBQueryCaller(t, ctx, r, mc2)

	tx1 := beginLockTx(t, ctx, inst1)
	var out1 dbLockOutput
	env := callHost(t, ctx, inst1, "call_lock", dbLockInput{Key: "shared-key", TxID: tx1, Shared: true})
	if !env.OK {
		t.Fatalf("first shared lock failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &out1); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out1.Acquired {
		t.Fatal("first caller failed to acquire a free shared lock")
	}

	tx2 := beginLockTx(t, ctx, inst2)
	var out2 dbLockOutput
	env = callHost(t, ctx, inst2, "call_lock", dbLockInput{Key: "shared-key", TxID: tx2, Shared: true})
	if !env.OK {
		t.Fatalf("second shared lock failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &out2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out2.Acquired {
		t.Error("second caller couldn't acquire a shared lock alongside the first — shared locks should coexist")
	}
}

// A lock_timeout cancellation must surface as Acquired: false, not a
// HostError, leaving the caller's transaction still usable afterward.
func TestHostDBLock_BlockingTimeout_ReturnsAcquiredFalseWithoutPoisoningTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dblocktimeouttest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc1 := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst1 := newHostDBQueryCaller(t, ctx, r, mc1)
	mc2 := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst2 := newHostDBQueryCaller(t, ctx, r, mc2)

	tx1 := beginLockTx(t, ctx, inst1)
	var out1 dbLockOutput
	env := callHost(t, ctx, inst1, "call_lock", dbLockInput{Key: "blocking-key", TxID: tx1})
	if !env.OK {
		t.Fatalf("first lock failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &out1); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out1.Acquired {
		t.Fatal("first caller failed to acquire a free lock")
	}

	tx2 := beginLockTx(t, ctx, inst2)
	var out2 dbLockOutput
	env = callHost(t, ctx, inst2, "call_lock", dbLockInput{Key: "blocking-key", TxID: tx2, TimeoutMs: 200})
	if !env.OK {
		t.Fatalf("blocking lock call failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &out2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out2.Acquired {
		t.Error("Acquired = true, want false — the first caller still holds this lock")
	}
	if out2.DurationMs < 150 {
		t.Errorf("DurationMs = %v, want roughly >= 200 (the requested timeout)", out2.DurationMs)
	}

	// The transaction must still be usable — proves ROLLBACK TO SAVEPOINT
	// un-aborted it after the lock_timeout cancellation.
	var out3 dbLockOutput
	env = callHost(t, ctx, inst2, "call_lock", dbLockInput{Key: "another-key", TxID: tx2})
	if !env.OK {
		t.Fatalf("lock call on the same transaction after a timeout failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &out3); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out3.Acquired {
		t.Error("expected tx2 to still be usable and acquire an unrelated free lock")
	}
	if env := callHost(t, ctx, inst2, "call_commit", dbTxIDInput{TxID: tx2}); !env.OK {
		t.Fatalf("commit tx2 failed after a lock timeout: %+v", env.Error)
	}
}

// An out-of-range TimeoutMs (Postgres's lock_timeout GUC only accepts
// 0..2147483647) fails the SET LOCAL statement itself, not the lock
// attempt — that failure must still roll back to the savepoint rather
// than leaving the transaction aborted for every subsequent call.
func TestHostDBLock_OutOfRangeTimeout_DoesNotPoisonTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dblockbadtimeouttest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	txID := beginLockTx(t, ctx, inst)

	env := callHost(t, ctx, inst, "call_lock", dbLockInput{Key: "bad-timeout-key", TxID: txID, TimeoutMs: -500})
	if env.OK {
		t.Fatal("expected an error for an out-of-range timeout_ms")
	}
	if env.Error.Code != abi.ErrCodeUnavailable {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeUnavailable)
	}

	// The transaction must still be usable — proves the failed SET LOCAL
	// rolled back to the savepoint instead of leaving the transaction
	// aborted.
	var out dbLockOutput
	env = callHost(t, ctx, inst, "call_lock", dbLockInput{Key: "unrelated-key", TxID: txID})
	if !env.OK {
		t.Fatalf("lock call on the same transaction after a bad timeout failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.Acquired {
		t.Error("expected the transaction to still be usable and acquire an unrelated free lock")
	}
	if env := callHost(t, ctx, inst, "call_commit", dbTxIDInput{TxID: txID}); !env.OK {
		t.Fatalf("commit failed after an out-of-range timeout: %+v", env.Error)
	}
}
