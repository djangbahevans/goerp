package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/vmihailenco/msgpack/v5"
)

// TestModuleContext_RawConn_SharesPhysicalConnectionWithTransaction proves
// goerp#511's core claim: work issued through RawConn's raw pgx handle
// participates in the same transaction Transaction's own *sql.Tx runs on,
// because both share one physical connection. An uncommitted INSERT made
// through the raw handle is only visible to the *sql.Tx's own query if
// they're genuinely the same backend transaction — a different connection
// (or even a different transaction on the same connection) would not see
// it under read-committed isolation.
func TestModuleContext_RawConn_SharesPhysicalConnectionWithTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("rawconnsharetest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	if _, err := primaryDB.ExecContext(ctx, "CREATE TABLE "+tenantschema.Name(slug)+".widget (name text)"); err != nil {
		t.Fatalf("create fixture table: %v", err)
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBCaller(t, ctx, r, mc)

	beginEnv := callHost(t, ctx, inst, "call_begin", dbBeginInput{})
	if !beginEnv.OK {
		t.Fatalf("begin failed: %+v", beginEnv.Error)
	}
	var beginOut dbBeginOutput
	if err := msgpack.Unmarshal(beginEnv.Data, &beginOut); err != nil {
		t.Fatalf("unmarshal begin output: %v", err)
	}
	txID := beginOut.TxID

	tx, ok := mc.Transaction(txID)
	if !ok {
		t.Fatal("Transaction: not found")
	}
	conn, ok := mc.RawConn(txID)
	if !ok {
		t.Fatal("RawConn: not found")
	}

	if err := conn.Raw(func(driverConn any) error {
		pgxConn := driverConn.(*stdlib.Conn).Conn()
		_, err := pgxConn.Exec(ctx, "INSERT INTO widget (name) VALUES ($1)", "via-raw-conn")
		return err
	}); err != nil {
		t.Fatalf("raw exec: %v", err)
	}

	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM widget WHERE name = $1", "via-raw-conn").Scan(&count); err != nil {
		t.Fatalf("query via sql.Tx: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 — the raw pgx INSERT and the *sql.Tx query must share the same uncommitted transaction", count)
	}

	if env := callHost(t, ctx, inst, "call_rollback", dbTxIDInput{TxID: txID}); !env.OK {
		t.Fatalf("rollback failed: %+v", env.Error)
	}
}

func TestModuleContext_RawConn_UnknownTxID(t *testing.T) {
	mc := newTestModuleContext("acme", abi.CapDBWrite, nil)

	if _, ok := mc.RawConn("does-not-exist"); ok {
		t.Error("RawConn(unknown) ok = true, want false")
	}
}

// connIsClosed reports whether conn has already been returned to the pool
// (RemoveTransaction/RollbackAll's own job) by attempting a trivial
// operation and checking for sql.ErrConnDone — the documented error every
// operation on an already-Close'd *sql.Conn returns.
func connIsClosed(ctx context.Context, conn *sql.Conn) bool {
	return errors.Is(conn.PingContext(ctx), sql.ErrConnDone)
}

func TestHostDBCommit_ReleasesPinnedConnBackToPool(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("connreleasecommittest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBCaller(t, ctx, r, mc)

	beginEnv := callHost(t, ctx, inst, "call_begin", dbBeginInput{})
	if !beginEnv.OK {
		t.Fatalf("begin failed: %+v", beginEnv.Error)
	}
	var beginOut dbBeginOutput
	if err := msgpack.Unmarshal(beginEnv.Data, &beginOut); err != nil {
		t.Fatalf("unmarshal begin output: %v", err)
	}

	conn, ok := mc.RawConn(beginOut.TxID)
	if !ok {
		t.Fatal("RawConn: not found")
	}
	if connIsClosed(ctx, conn) {
		t.Fatal("conn already closed before commit")
	}

	if env := callHost(t, ctx, inst, "call_commit", dbTxIDInput{TxID: beginOut.TxID}); !env.OK {
		t.Fatalf("commit failed: %+v", env.Error)
	}

	if !connIsClosed(ctx, conn) {
		t.Error("conn still usable after commit — host.db.commit must close the pinned *sql.Conn, not just tx.Commit()")
	}
}

func TestHostDBRollback_ReleasesPinnedConnBackToPool(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("connreleaserollbacktest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBCaller(t, ctx, r, mc)

	beginEnv := callHost(t, ctx, inst, "call_begin", dbBeginInput{})
	if !beginEnv.OK {
		t.Fatalf("begin failed: %+v", beginEnv.Error)
	}
	var beginOut dbBeginOutput
	if err := msgpack.Unmarshal(beginEnv.Data, &beginOut); err != nil {
		t.Fatalf("unmarshal begin output: %v", err)
	}

	conn, ok := mc.RawConn(beginOut.TxID)
	if !ok {
		t.Fatal("RawConn: not found")
	}

	if env := callHost(t, ctx, inst, "call_rollback", dbTxIDInput{TxID: beginOut.TxID}); !env.OK {
		t.Fatalf("rollback failed: %+v", env.Error)
	}

	if !connIsClosed(ctx, conn) {
		t.Error("conn still usable after rollback — host.db.rollback must close the pinned *sql.Conn, not just tx.Rollback()")
	}
}

// TestModuleContext_RollbackAll_ReleasesPinnedConn simulates the
// dispatch-path safety net (invokeHandler's defer) draining a transaction
// a module handler never explicitly committed or rolled back — distinct
// from the explicit host.db.commit/host.db.rollback paths the two tests
// above cover.
func TestModuleContext_RollbackAll_ReleasesPinnedConn(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("rollbackallconntest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	mc := newTestModuleContext(slug, abi.CapDBWrite, nil)
	tx := registerTenantScopedTestTx(t, ctx, primaryDB, mc, "abandoned-tx")

	conn, ok := mc.RawConn("abandoned-tx")
	if !ok {
		t.Fatal("RawConn: not found")
	}

	mc.RollbackAll()

	if !connIsClosed(ctx, conn) {
		t.Error("conn still usable after RollbackAll — the safety net must close every pinned *sql.Conn it drains")
	}
	if _, ok := mc.Transaction("abandoned-tx"); ok {
		t.Error("transaction still registered after RollbackAll")
	}

	// tx itself is already unusable (its connection was closed out from
	// under it) — this just documents that RollbackAll owns ending it,
	// not a further assertion.
	_ = tx
}
