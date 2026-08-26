package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

var hostEventCallerModule = buildHostCallerModule("host.event", []string{"emit_tx", "emit"})

func newHostEventCaller(t *testing.T, ctx context.Context, r *Runtime, mc *ModuleContext) *ModuleInstance {
	t.Helper()

	compiled, err := r.wazero.CompileModule(ctx, hostEventCallerModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("event-caller-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	return inst
}

func newEmitterEventRegistry(moduleName, eventName, idempotencyKeyField string) *event.EventRegistry {
	reg := event.NewEventRegistry()
	reg.Register(moduleName, manifest.Manifest{
		Name: moduleName,
		Emits: []manifest.EventDeclaration{
			{Name: eventName, Version: 1, IdempotencyKeyField: idempotencyKeyField},
		},
	})
	return reg
}

func newEventTestModuleContext(reg *event.EventRegistry) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, "tenant-id-1", "eventtest", "trace-1", abi.CapEventEmit, nil, ModuleSnapshot{EventRegistry: reg})
}

// beginAndRegisterTx opens a real transaction on primaryDB and registers
// it on mc under a fresh tx_id — the same bookkeeping host.db.begin does,
// done directly here since these tests only need emit_tx's own side, not
// a full host.db ABI round trip.
func beginAndRegisterTx(t *testing.T, ctx context.Context, primaryDB *sql.DB, mc *ModuleContext) (txID string, tx *sql.Tx) {
	t.Helper()
	tx, err := primaryDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	txID = uuid.NewString()
	mc.RegisterTransaction(txID, tx)
	return txID, tx
}

func countEventDeliveryJobs(t *testing.T, conn *sql.DB, tenantID string) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(
		`SELECT count(*) FROM river_job WHERE kind = 'event_delivery' AND args->>'tenant_id' = $1`,
		tenantID,
	).Scan(&count); err != nil {
		t.Fatalf("count river_job rows: %v", err)
	}
	return count
}

func TestHostEvent_EmitTx_InsertsJobOnlyOnCommit(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	tenantID := uuid.NewString()
	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, tenantID, "eventcommittest", "trace-1", abi.CapEventEmit, nil, ModuleSnapshot{EventRegistry: reg})

	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostEventCaller(t, ctx, r, mc)

	// Rolled-back transaction: emit_tx succeeds (job insert happens inside
	// the tx), but the rollback must take the job insert down with it.
	rollbackTxID, rollbackTx := beginAndRegisterTx(t, ctx, primaryDB, mc)
	env := callHost(t, ctx, inst, "call_emit_tx", eventEmitTxInput{TxID: rollbackTxID, Name: "sales.order.confirmed", Payload: mustMarshalPayload(t, map[string]any{"order_id": "1"})})
	if !env.OK {
		t.Fatalf("emit_tx (rollback case) failed: %+v", env.Error)
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if got := countEventDeliveryJobs(t, primaryDB, tenantID); got != 0 {
		t.Fatalf("job count after rollback = %d, want 0", got)
	}

	// Committed transaction: the job must be visible afterward.
	commitTxID, commitTx := beginAndRegisterTx(t, ctx, primaryDB, mc)
	env = callHost(t, ctx, inst, "call_emit_tx", eventEmitTxInput{TxID: commitTxID, Name: "sales.order.confirmed", Payload: mustMarshalPayload(t, map[string]any{"order_id": "2"})})
	if !env.OK {
		t.Fatalf("emit_tx (commit case) failed: %+v", env.Error)
	}
	if err := commitTx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if got := countEventDeliveryJobs(t, primaryDB, tenantID); got != 1 {
		t.Fatalf("job count after commit = %d, want 1", got)
	}
}

func TestHostEvent_EmitTx_NoTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
	mc := newEventTestModuleContext(reg)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostEventCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_emit_tx", eventEmitTxInput{TxID: "does-not-exist", Name: "sales.order.confirmed"})
	if env.OK {
		t.Fatal("expected an error for a missing tx_id")
	}
	if env.Error.Code != abi.ErrCodeNoTransaction {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeNoTransaction)
	}
}

func TestHostEvent_EmitTx_UndeclaredEvent(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
	mc := newEventTestModuleContext(reg)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostEventCaller(t, ctx, r, mc)

	txID, tx := beginAndRegisterTx(t, ctx, primaryDB, mc)
	defer func() { _ = tx.Rollback() }()

	env := callHost(t, ctx, inst, "call_emit_tx", eventEmitTxInput{TxID: txID, Name: "sales.order.not_declared"})
	if env.OK {
		t.Fatal("expected an error for an undeclared event name")
	}
	if env.Error.Code != abi.ErrCodeUndeclared {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeUndeclared)
	}
}

func TestHostEvent_EmitTx_CapabilityDenied(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, "tenant-id-1", "eventnocaptest", "trace-1", abi.CapabilitySet(0), nil, ModuleSnapshot{EventRegistry: reg})
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostEventCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_emit_tx", eventEmitTxInput{TxID: "irrelevant", Name: "sales.order.confirmed"})
	if env.OK {
		t.Fatal("expected an error without event.emit capability")
	}
	if env.Error.Code != abi.ErrCodeCapabilityDenied {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeCapabilityDenied)
	}
}

func TestHostEvent_EmitTx_ExplicitIdempotencyKey_DedupesAcrossCalls(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	tenantID := uuid.NewString()
	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, tenantID, "eventdeduptest", "trace-1", abi.CapEventEmit, nil, ModuleSnapshot{EventRegistry: reg})
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostEventCaller(t, ctx, r, mc)

	const key = "retry-key-1"
	var firstEventID, secondEventID string

	for i, out := range []*string{&firstEventID, &secondEventID} {
		txID, tx := beginAndRegisterTx(t, ctx, primaryDB, mc)
		env := callHost(t, ctx, inst, "call_emit_tx", eventEmitTxInput{
			TxID: txID, Name: "sales.order.confirmed", IdempotencyKey: key,
			Payload: mustMarshalPayload(t, map[string]any{"attempt": i}),
		})
		if !env.OK {
			t.Fatalf("emit_tx call %d failed: %+v", i, env.Error)
		}
		var result eventEmitTxOutput
		if err := msgpack.Unmarshal(env.Data, &result); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		*out = result.EventID
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	if firstEventID != secondEventID {
		t.Fatalf("event_id differed across calls with the same idempotency_key: %q vs %q", firstEventID, secondEventID)
	}
	if got := countEventDeliveryJobs(t, primaryDB, tenantID); got != 1 {
		t.Fatalf("job count = %d, want 1 (deduped)", got)
	}
}

func TestHostEvent_EmitTx_ManifestIdempotencyKeyField_DedupesFromPayload(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	tenantID := uuid.NewString()
	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "order_id")
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, tenantID, "eventmanifestdeduptest", "trace-1", abi.CapEventEmit, nil, ModuleSnapshot{EventRegistry: reg})
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostEventCaller(t, ctx, r, mc)

	// No explicit IdempotencyKey — resolved from the payload's order_id
	// field via the manifest's declared idempotency_key_field.
	for i := range 2 {
		txID, tx := beginAndRegisterTx(t, ctx, primaryDB, mc)
		env := callHost(t, ctx, inst, "call_emit_tx", eventEmitTxInput{
			TxID: txID, Name: "sales.order.confirmed",
			Payload: mustMarshalPayload(t, map[string]any{"order_id": "ORD-42"}),
		})
		if !env.OK {
			t.Fatalf("emit_tx call %d failed: %+v", i, env.Error)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	if got := countEventDeliveryJobs(t, primaryDB, tenantID); got != 1 {
		t.Fatalf("job count = %d, want 1 (deduped via manifest idempotency_key_field)", got)
	}
}

func TestHostEvent_EmitTx_NoIdempotencyKey_NoDedup(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	tenantID := uuid.NewString()
	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, tenantID, "eventnodeduptest", "trace-1", abi.CapEventEmit, nil, ModuleSnapshot{EventRegistry: reg})
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostEventCaller(t, ctx, r, mc)

	for i := range 2 {
		txID, tx := beginAndRegisterTx(t, ctx, primaryDB, mc)
		env := callHost(t, ctx, inst, "call_emit_tx", eventEmitTxInput{TxID: txID, Name: "sales.order.confirmed"})
		if !env.OK {
			t.Fatalf("emit_tx call %d failed: %+v", i, env.Error)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	if got := countEventDeliveryJobs(t, primaryDB, tenantID); got != 2 {
		t.Fatalf("job count = %d, want 2 (no idempotency key means no dedup)", got)
	}
}

func mustMarshalPayload(t *testing.T, v map[string]any) []byte {
	t.Helper()
	data, err := msgpack.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}

func TestHostEvent_EmitTx_RejectsSync(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
	mc := newEventTestModuleContext(reg)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostEventCaller(t, ctx, r, mc)

	txID, tx := beginAndRegisterTx(t, ctx, primaryDB, mc)
	defer func() { _ = tx.Rollback() }()

	env := callHost(t, ctx, inst, "call_emit_tx", eventEmitTxInput{TxID: txID, Name: "sales.order.confirmed", Sync: true})
	if env.OK {
		t.Fatal("expected an error rejecting WithSync() on EmitTx")
	}
	if env.Error.Code != abi.ErrCodeSyncNotAllowed {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeSyncNotAllowed)
	}
}

func TestHostEvent_Emit_InsertsJobWithoutTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	tenantID := uuid.NewString()
	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, tenantID, "eventemittest", "trace-1", abi.CapEventEmit, nil, ModuleSnapshot{EventRegistry: reg})
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostEventCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_emit", eventEmitInput{Name: "sales.order.confirmed", Payload: mustMarshalPayload(t, map[string]any{"order_id": "1"})})
	if !env.OK {
		t.Fatalf("emit failed: %+v", env.Error)
	}

	if got := countEventDeliveryJobs(t, primaryDB, tenantID); got != 1 {
		t.Fatalf("job count = %d, want 1", got)
	}
}

func TestHostEvent_Emit_UndeclaredEvent(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
	mc := newEventTestModuleContext(reg)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostEventCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_emit", eventEmitInput{Name: "sales.order.not_declared"})
	if env.OK {
		t.Fatal("expected an error for an undeclared event name")
	}
	if env.Error.Code != abi.ErrCodeUndeclared {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeUndeclared)
	}
}

func TestHostEvent_Emit_CapabilityDenied(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, "tenant-id-1", "eventemitnocaptest", "trace-1", abi.CapabilitySet(0), nil, ModuleSnapshot{EventRegistry: reg})
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostEventCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_emit", eventEmitInput{Name: "sales.order.confirmed"})
	if env.OK {
		t.Fatal("expected an error without event.emit capability")
	}
	if env.Error.Code != abi.ErrCodeCapabilityDenied {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeCapabilityDenied)
	}
}

// fakeSyncEventDispatcher lets tests control DispatchSync's outcome per
// (moduleName, handlerName) without a real WASM subscriber module.
type fakeSyncEventDispatcher struct {
	results map[string]struct {
		status int32
		err    error
	}
	calls []string
}

func (f *fakeSyncEventDispatcher) DispatchSync(ctx context.Context, moduleName, handlerName string, payload []byte) (int32, error) {
	f.calls = append(f.calls, moduleName+"."+handlerName)
	if r, ok := f.results[moduleName+"."+handlerName]; ok {
		return r.status, r.err
	}
	return 0, nil
}

func newSyncTestModuleContext(reg *event.EventRegistry, tenantID string) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, tenantID, "eventsynctest", "trace-1", abi.CapEventEmit, nil, ModuleSnapshot{EventRegistry: reg})
}

func newEmitterAndSubscriberRegistry(emitterModule, eventName string, subs ...manifest.EventSubscription) *event.EventRegistry {
	reg := event.NewEventRegistry()
	reg.Register(emitterModule, manifest.Manifest{
		Name:  emitterModule,
		Emits: []manifest.EventDeclaration{{Name: eventName, Version: 1}},
	})
	for i, sub := range subs {
		reg.Register(fmt.Sprintf("subscriber-%d", i), manifest.Manifest{Subscribes: []manifest.EventSubscription{sub}})
	}
	return reg
}

func TestHostEvent_Emit_Sync_AllSubscribersSucceed(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	tenantID := uuid.NewString()
	reg := newEmitterAndSubscriberRegistry("testmodule", "sales.order.shipped",
		manifest.EventSubscription{Name: "sales.order.shipped", Handler: "handle_a", Async: false},
	)
	mc := newSyncTestModuleContext(reg, tenantID)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	dispatcher := &fakeSyncEventDispatcher{}
	r.SetSyncEventDispatcher(dispatcher)
	inst := newHostEventCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_emit", eventEmitInput{Name: "sales.order.shipped", Sync: true, Payload: mustMarshalPayload(t, map[string]any{})})
	if !env.OK {
		t.Fatalf("emit (sync) failed: %+v", env.Error)
	}
	if len(dispatcher.calls) != 1 {
		t.Fatalf("expected exactly 1 sync subscriber dispatched, got %v", dispatcher.calls)
	}
	if got := countEventDeliveryJobs(t, primaryDB, tenantID); got != 1 {
		t.Fatalf("job count = %d, want 1 (still inserted for the audit row/async subscribers)", got)
	}
}

func TestHostEvent_Emit_Sync_SubscriberFailureAggregatedAndReturned(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	tenantID := uuid.NewString()
	reg := newEmitterAndSubscriberRegistry("testmodule", "sales.order.shipped",
		manifest.EventSubscription{Name: "sales.order.shipped", Handler: "handle_a", Async: false},
		manifest.EventSubscription{Name: "sales.order.shipped", Handler: "handle_b", Async: false},
	)
	mc := newSyncTestModuleContext(reg, tenantID)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	dispatcher := &fakeSyncEventDispatcher{results: map[string]struct {
		status int32
		err    error
	}{
		"subscriber-0.handle_a": {status: 1},
	}}
	r.SetSyncEventDispatcher(dispatcher)
	inst := newHostEventCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_emit", eventEmitInput{Name: "sales.order.shipped", Sync: true})
	if env.OK {
		t.Fatal("expected an aggregated dispatch failure")
	}
	if env.Error.Code != abi.ErrCodeDispatchFailed {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeDispatchFailed)
	}
	// Both subscribers must still have been attempted — a failing
	// subscriber must not stop the remaining ones from running.
	if len(dispatcher.calls) != 2 {
		t.Fatalf("expected both subscribers dispatched despite one failing, got %v", dispatcher.calls)
	}
}
