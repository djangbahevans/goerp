package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/vmihailenco/msgpack/v5"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/role's and internal/engine/
// tenant's tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// hostDBCallerModule is a hand-assembled WASM module that imports
// host.db.begin/commit/rollback and re-exports each as call_begin/
// call_commit/call_rollback, forwarding (ptr, len) straight through and
// returning the packed i64 result unchanged — enough to exercise the real
// wazero function-attachment path (registerHostDB), not just the Go
// closures directly. allocate/deallocate reuse the same bump-allocator/
// no-op convention established in invoke_test.go's boundaryTestModule.
var hostDBCallerModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x11, 0x03, 0x60,
	0x01, 0x7F, 0x01, 0x7F, 0x60, 0x02, 0x7F, 0x7F, 0x00, 0x60, 0x02, 0x7F,
	0x7F, 0x01, 0x7E, 0x02, 0x35, 0x03, 0x07, 0x68, 0x6F, 0x73, 0x74, 0x2E,
	0x64, 0x62, 0x05, 0x62, 0x65, 0x67, 0x69, 0x6E, 0x00, 0x02, 0x07, 0x68,
	0x6F, 0x73, 0x74, 0x2E, 0x64, 0x62, 0x06, 0x63, 0x6F, 0x6D, 0x6D, 0x69,
	0x74, 0x00, 0x02, 0x07, 0x68, 0x6F, 0x73, 0x74, 0x2E, 0x64, 0x62, 0x08,
	0x72, 0x6F, 0x6C, 0x6C, 0x62, 0x61, 0x63, 0x6B, 0x00, 0x02, 0x03, 0x06,
	0x05, 0x00, 0x01, 0x02, 0x02, 0x02, 0x05, 0x03, 0x01, 0x00, 0x01, 0x06,
	0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B, 0x07, 0x44, 0x05, 0x08,
	0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x03, 0x0A, 0x64,
	0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x04, 0x0A,
	0x63, 0x61, 0x6C, 0x6C, 0x5F, 0x62, 0x65, 0x67, 0x69, 0x6E, 0x00, 0x05,
	0x0B, 0x63, 0x61, 0x6C, 0x6C, 0x5F, 0x63, 0x6F, 0x6D, 0x6D, 0x69, 0x74,
	0x00, 0x06, 0x0D, 0x63, 0x61, 0x6C, 0x6C, 0x5F, 0x72, 0x6F, 0x6C, 0x6C,
	0x62, 0x61, 0x63, 0x6B, 0x00, 0x07, 0x0A, 0x31, 0x05, 0x11, 0x01, 0x01,
	0x7F, 0x23, 0x00, 0x21, 0x01, 0x20, 0x01, 0x20, 0x00, 0x6A, 0x24, 0x00,
	0x20, 0x01, 0x0B, 0x02, 0x00, 0x0B, 0x08, 0x00, 0x20, 0x00, 0x20, 0x01,
	0x10, 0x00, 0x0B, 0x08, 0x00, 0x20, 0x00, 0x20, 0x01, 0x10, 0x01, 0x0B,
	0x08, 0x00, 0x20, 0x00, 0x20, 0x01, 0x10, 0x02, 0x0B,
}

// wireEnvelope mirrors abi's unexported envelope type structurally (same
// msgpack field names) so tests outside the abi package can decode a host
// function's response without abi exporting an internal wire type.
type wireEnvelope struct {
	OK    bool               `msgpack:"ok"`
	Data  msgpack.RawMessage `msgpack:"data,omitempty"`
	Error *abi.HostError     `msgpack:"error,omitempty"`
}

func openTestPrimaryDB(t *testing.T) *sql.DB {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func newHostDBTestRuntime(t *testing.T, primaryDB *sql.DB, maxConcurrentTx int) *Runtime {
	t.Helper()

	rt, err := New(&config.Config{
		CompilationCache:            filepath.Join(t.TempDir(), "cache"),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           1 << 20,
		DBMaxConcurrentTransactions: maxConcurrentTx,
		SyncSubscriberTimeout:       3 * time.Second,
	}, primaryDB, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

// newHostDBCaller compiles and instantiates hostDBCallerModule against r's
// own wazero runtime (imports only resolve within one wazero.Runtime) and
// registers it under r, so InstanceForModule finds it the same way a real
// module dispatched through invokeHandler would be found.
func newHostDBCaller(t *testing.T, ctx context.Context, r *Runtime, mc *ModuleContext) *ModuleInstance {
	t.Helper()

	compiled, err := r.wazero.CompileModule(ctx, hostDBCallerModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("caller-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	return inst
}

// callHost writes req into the caller's own linear memory via its allocate
// export, invokes exportName (call_begin/call_commit/call_rollback), and
// decodes the packed ptr/len result back into a wireEnvelope.
func callHost(t *testing.T, ctx context.Context, inst *ModuleInstance, exportName string, req any) wireEnvelope {
	t.Helper()

	payload, err := msgpack.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	allocRes, err := inst.allocate.Call(ctx, uint64(len(payload)))
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	ptr := uint32(allocRes[0])
	if !inst.module.Memory().Write(ptr, payload) {
		t.Fatalf("memory.Write out of bounds")
	}

	results, err := inst.module.ExportedFunction(exportName).Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		t.Fatalf("call %s: %v", exportName, err)
	}

	packed := results[0]
	respPtr := uint32(packed >> 32)
	respLen := uint32(packed)
	respBytes, ok := inst.module.Memory().Read(respPtr, respLen)
	if !ok {
		t.Fatalf("memory.Read out of bounds at ptr=%d len=%d", respPtr, respLen)
	}

	var env wireEnvelope
	if err := msgpack.Unmarshal(respBytes, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	return env
}

func newTestModuleContext(tenantSlug string, caps abi.CapabilitySet, txLimiter *TransactionLimiter) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, "tenant-id-1", tenantSlug, "trace-1", caps, txLimiter, ModuleSnapshot{})
}

func createFixtureTenantSchema(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schema := tenantschema.Name(slug)

	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})
}

func TestHostDB_BeginCommit_SetsSearchPathAndCommits(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbtest%d", time.Now().UnixNano())
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
	if beginOut.TxID == "" {
		t.Fatal("expected a non-empty tx_id")
	}

	tx, ok := mc.Transaction(beginOut.TxID)
	if !ok {
		t.Fatal("expected the transaction to be registered on the ModuleContext")
	}
	var searchPath string
	if err := tx.QueryRowContext(ctx, "SHOW search_path").Scan(&searchPath); err != nil {
		t.Fatalf("SHOW search_path: %v", err)
	}
	want := "tenant_" + slug + ", public"
	if searchPath != want {
		t.Errorf("search_path = %q, want %q", searchPath, want)
	}

	commitEnv := callHost(t, ctx, inst, "call_commit", dbTxIDInput{TxID: beginOut.TxID})
	if !commitEnv.OK {
		t.Fatalf("commit failed: %+v", commitEnv.Error)
	}

	if _, ok := mc.Transaction(beginOut.TxID); ok {
		t.Error("expected tx_id to be forgotten after a successful commit")
	}
}

func TestHostDB_Rollback_AfterCommitIsNoopSuccess(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBCaller(t, ctx, r, mc)

	beginEnv := callHost(t, ctx, inst, "call_begin", dbBeginInput{})
	var beginOut dbBeginOutput
	_ = msgpack.Unmarshal(beginEnv.Data, &beginOut)

	commitEnv := callHost(t, ctx, inst, "call_commit", dbTxIDInput{TxID: beginOut.TxID})
	if !commitEnv.OK {
		t.Fatalf("commit failed: %+v", commitEnv.Error)
	}

	rollbackEnv := callHost(t, ctx, inst, "call_rollback", dbTxIDInput{TxID: beginOut.TxID})
	if !rollbackEnv.OK {
		t.Errorf("rollback after commit should be a no-op success, got error %+v", rollbackEnv.Error)
	}
}

func TestHostDB_Begin_NestedReturnsAlreadyOpen(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBCaller(t, ctx, r, mc)

	first := callHost(t, ctx, inst, "call_begin", dbBeginInput{})
	if !first.OK {
		t.Fatalf("first begin failed: %+v", first.Error)
	}

	second := callHost(t, ctx, inst, "call_begin", dbBeginInput{})
	if second.OK {
		t.Fatal("expected nested begin to fail")
	}
	if second.Error.Code != abi.ErrCodeTransactionAlreadyOpen {
		t.Errorf("error code = %q, want %q", second.Error.Code, abi.ErrCodeTransactionAlreadyOpen)
	}

	var beginOut dbBeginOutput
	_ = msgpack.Unmarshal(first.Data, &beginOut)
	callHost(t, ctx, inst, "call_rollback", dbTxIDInput{TxID: beginOut.TxID})
}

func TestHostDB_Begin_CapabilityDenied(t *testing.T) {
	r := newHostDBTestRuntime(t, nil, 10)
	mc := newTestModuleContext("acme", 0, r.TxLimiter())
	ctx := context.Background()
	inst := newHostDBCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_begin", dbBeginInput{})
	if env.OK {
		t.Fatal("expected capability_denied for a module without CapDBWrite")
	}
	if env.Error.Code != abi.ErrCodeCapabilityDenied {
		t.Errorf("error code = %q, want %q", env.Error.Code, abi.ErrCodeCapabilityDenied)
	}
}

func TestHostDB_Begin_TransactionLimitExceeded(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 1)

	mc1 := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst1 := newHostDBCaller(t, ctx, r, mc1)
	first := callHost(t, ctx, inst1, "call_begin", dbBeginInput{})
	if !first.OK {
		t.Fatalf("first begin failed: %+v", first.Error)
	}

	mc2 := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst2 := newHostDBCaller(t, ctx, r, mc2)
	second := callHost(t, ctx, inst2, "call_begin", dbBeginInput{})
	if second.OK {
		t.Fatal("expected the engine-wide transaction limit to be enforced")
	}
	if second.Error.Code != abi.ErrCodeTransactionLimitExceeded {
		t.Errorf("error code = %q, want %q", second.Error.Code, abi.ErrCodeTransactionLimitExceeded)
	}

	var beginOut dbBeginOutput
	_ = msgpack.Unmarshal(first.Data, &beginOut)
	callHost(t, ctx, inst1, "call_rollback", dbTxIDInput{TxID: beginOut.TxID})
}

func TestModuleContext_RollbackAll_ReleasesLimiterSlot(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 1)
	mc := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBCaller(t, ctx, r, mc)

	begin := callHost(t, ctx, inst, "call_begin", dbBeginInput{})
	if !begin.OK {
		t.Fatalf("begin failed: %+v", begin.Error)
	}

	if r.TxLimiter().TryAcquire() {
		t.Fatal("expected the limiter to be exhausted before RollbackAll")
	}

	mc.RollbackAll()

	if !r.TxLimiter().TryAcquire() {
		t.Fatal("expected RollbackAll to release the limiter slot")
	}
	r.TxLimiter().Release()
}
