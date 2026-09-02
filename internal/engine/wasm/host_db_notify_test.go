package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// testListener LISTENs on a single Postgres channel over its own pinned
// connection — LISTEN is connection-scoped, so it can't share primaryDB's
// pool the way every other host.db test does.
type testListener struct {
	conn *sql.Conn
}

// listenOnChannel pins a connection from primaryDB and issues LISTEN on
// channel, quoted via pgx.Identifier since a tenant-prefixed channel name
// (e.g. "acme:orders") isn't a bare Postgres identifier.
func listenOnChannel(t *testing.T, primaryDB *sql.DB, channel string) *testListener {
	t.Helper()
	ctx := context.Background()

	conn, err := primaryDB.Conn(ctx)
	if err != nil {
		t.Fatalf("pin connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if _, err := conn.ExecContext(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		t.Fatalf("LISTEN %s: %v", channel, err)
	}
	return &testListener{conn: conn}
}

// waitForNotification blocks up to timeout for one notification on l's
// channel, returning nil (not a test failure) if none arrives in time —
// callers assert on the result themselves, since "no notification arrived"
// is the expected outcome for several of this file's tests.
func (l *testListener) waitForNotification(t *testing.T, timeout time.Duration) *pgconn.Notification {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var notification *pgconn.Notification
	err := l.conn.Raw(func(driverConn any) error {
		pgxConn := driverConn.(*stdlib.Conn).Conn()
		n, err := pgxConn.WaitForNotification(ctx)
		notification = n
		return err
	})
	if err != nil {
		return nil
	}
	return notification
}

func TestHostDBNotify_Immediate_DeliversToListener(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dbnotifyimmediatetest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	listener := listenOnChannel(t, primaryDB, slug+":orders")

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBNotify, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	var out dbDurationOutput
	env := callHost(t, ctx, inst, "call_notify", dbNotifyInput{Channel: "orders", Payload: "order-123"})
	if !env.OK {
		t.Fatalf("notify failed: %+v", env.Error)
	}
	if err := unmarshalEnvelope(t, env, &out); err != nil {
		t.Fatalf("unmarshal notify output: %v", err)
	}

	n := listener.waitForNotification(t, 5*time.Second)
	if n == nil {
		t.Fatal("expected a notification, got none within the timeout")
	}
	if n.Payload != "order-123" {
		t.Errorf("Payload = %q, want %q", n.Payload, "order-123")
	}
}

func TestHostDBNotify_CapabilityDenied(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dbnotifycaptest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter()) // no CapDBNotify
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_notify", dbNotifyInput{Channel: "orders", Payload: "irrelevant"})
	if env.OK {
		t.Fatal("expected capability denial without db.notify")
	}
	if env.Error.Code != abi.ErrCodeCapabilityDenied {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeCapabilityDenied)
	}
}

func TestHostDBNotify_TxIDNotFound(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dbnotifytxnotfoundtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBNotify, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_notify", dbNotifyInput{Channel: "orders", Payload: "irrelevant", TxID: "does-not-exist"})
	if env.OK {
		t.Fatal("expected an error for an unregistered tx_id")
	}
	if env.Error.Code != abi.ErrCodeTransactionNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeTransactionNotFound)
	}
}

func TestHostDBNotify_TenantNamespacing_DifferentTenantsDontCollide(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slugA := fmt.Sprintf("dbnotifytenanta%d", time.Now().UnixNano())
	slugB := fmt.Sprintf("dbnotifytenantb%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slugA)
	createFixtureTenantSchema(t, primaryDB, slugB)

	// Listening on tenant B's own prefixed channel — tenant A notifying
	// the same raw channel name must never land here.
	listenerB := listenOnChannel(t, primaryDB, slugB+":same-channel")

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mcA := newTestModuleContext(slugA, abi.CapDBNotify, r.TxLimiter())
	instA := newHostDBQueryCaller(t, ctx, r, mcA)

	env := callHost(t, ctx, instA, "call_notify", dbNotifyInput{Channel: "same-channel", Payload: "tenant-a-payload"})
	if !env.OK {
		t.Fatalf("tenant A notify failed: %+v", env.Error)
	}

	if n := listenerB.waitForNotification(t, 500*time.Millisecond); n != nil {
		t.Errorf("tenant B's listener received tenant A's notification (payload %q) — namespacing isn't isolating tenants", n.Payload)
	}
}

func TestHostDBNotify_TxDeferred_DeliveredOnlyAfterCommit(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dbnotifydeferredtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	listener := listenOnChannel(t, primaryDB, slug+":orders")

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBNotify|abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	txID := beginLockTx(t, ctx, inst)

	env := callHost(t, ctx, inst, "call_notify", dbNotifyInput{Channel: "orders", Payload: "deferred-payload", TxID: txID})
	if !env.OK {
		t.Fatalf("notify failed: %+v", env.Error)
	}

	if n := listener.waitForNotification(t, 500*time.Millisecond); n != nil {
		t.Fatalf("notification delivered before commit (payload %q) — Postgres should defer it", n.Payload)
	}

	if env := callHost(t, ctx, inst, "call_commit", dbTxIDInput{TxID: txID}); !env.OK {
		t.Fatalf("commit failed: %+v", env.Error)
	}

	n := listener.waitForNotification(t, 5*time.Second)
	if n == nil {
		t.Fatal("expected a notification after commit, got none within the timeout")
	}
	if n.Payload != "deferred-payload" {
		t.Errorf("Payload = %q, want %q", n.Payload, "deferred-payload")
	}
}

// An oversized payload (Postgres caps NOTIFY at 8000 bytes) must fail
// with a non-retryable error and must not poison the caller's own
// transaction — the same concern host.db.lock's own SAVEPOINT protects
// against for its own risky ExecContext calls.
func TestHostDBNotify_OversizedPayload_DoesNotPoisonTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dbnotifyoversizedtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBNotify|abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	txID := beginLockTx(t, ctx, inst)

	oversized := make([]byte, 9000)
	for i := range oversized {
		oversized[i] = 'x'
	}
	env := callHost(t, ctx, inst, "call_notify", dbNotifyInput{Channel: "orders", Payload: string(oversized), TxID: txID})
	if env.OK {
		t.Fatal("expected an error for an oversized payload")
	}
	if env.Error.Code != abi.ErrCodeExecError {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeExecError)
	}
	if env.Error.Retry {
		t.Error("Retry = true, want false — an oversized payload can never succeed on retry")
	}

	// The transaction must still be usable — proves the SAVEPOINT rolled
	// back the failed pg_notify call instead of leaving the transaction
	// aborted for every subsequent call.
	env = callHost(t, ctx, inst, "call_notify", dbNotifyInput{Channel: "orders", Payload: "fits-fine", TxID: txID})
	if !env.OK {
		t.Fatalf("notify on the same transaction after an oversized payload failed: %+v", env.Error)
	}
	if env := callHost(t, ctx, inst, "call_commit", dbTxIDInput{TxID: txID}); !env.OK {
		t.Fatalf("commit failed after an oversized payload: %+v", env.Error)
	}
}

// A transaction already left aborted by some earlier, unrelated failed
// statement must report notify's own failure without Retry: true —
// retrying the identical call against the same still-aborted transaction
// can never succeed without an explicit rollback first.
func TestHostDBNotify_AlreadyAbortedTransaction_DoesNotClaimRetryable(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dbnotifyabortedtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBNotify|abi.CapDBWrite|abi.CapDBRead, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	txID := beginLockTx(t, ctx, inst)

	// Poison the transaction with an unrelated failing statement, the way
	// a module's own earlier call might.
	if env := callHost(t, ctx, inst, "call_query", dbQueryInput{SQL: "SELECT 1/0", TxID: txID}); env.OK {
		t.Fatal("expected the division-by-zero query to fail")
	}

	env := callHost(t, ctx, inst, "call_notify", dbNotifyInput{Channel: "orders", Payload: "irrelevant", TxID: txID})
	if env.OK {
		t.Fatal("expected notify to fail on an already-aborted transaction")
	}
	if env.Error.Retry {
		t.Error("Retry = true, want false — retrying against a still-aborted transaction can never succeed")
	}
}

func TestHostDBNotify_TxDeferred_DroppedOnRollback(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("dbnotifyrollbacktest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	listener := listenOnChannel(t, primaryDB, slug+":orders")

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBNotify|abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	txID := beginLockTx(t, ctx, inst)

	env := callHost(t, ctx, inst, "call_notify", dbNotifyInput{Channel: "orders", Payload: "rolled-back-payload", TxID: txID})
	if !env.OK {
		t.Fatalf("notify failed: %+v", env.Error)
	}

	if env := callHost(t, ctx, inst, "call_rollback", dbTxIDInput{TxID: txID}); !env.OK {
		t.Fatalf("rollback failed: %+v", env.Error)
	}

	if n := listener.waitForNotification(t, 500*time.Millisecond); n != nil {
		t.Errorf("notification delivered after rollback (payload %q) — Postgres should drop it", n.Payload)
	}
}
