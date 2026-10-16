package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
)

func newEventTestDB(t *testing.T) *sql.DB {
	t.Helper()

	conn := membershiptest.NewUnbootstrappedDB(t)
	migrator, err := rivermigrate.New(riverdatabasesql.New(conn), &rivermigrate.Config{Schema: "system"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := migrator.Migrate(t.Context(), rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}

	return conn
}

func newEventValidationCaller(t *testing.T, primaryDB *sql.DB, reg *event.EventRegistry) (*Runtime, *ModuleContext, *ModuleInstance) {
	t.Helper()

	r, err := New(&config.Config{
		CompilationCache:            wasmtest.SharedCompilationCacheDir(),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           8 << 20,
		DBMaxConcurrentTransactions: 10,
	}, primaryDB, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = r.Close(context.Background()) })
	mc := newSyncTestModuleContext(reg, uuid.NewV7().String())
	inst := newHostEventCaller(t, t.Context(), r, mc)
	if _, ok := inst.module.Memory().Grow(32); !ok {
		t.Fatal("grow caller memory")
	}

	return r, mc, inst
}

func TestHostEvent_PayloadSizeBoundary(t *testing.T) {
	primaryDB := newEventTestDB(t)
	for _, transactional := range []bool{false, true} {
		for _, size := range []int{event.MaxPayloadBytes - 1, event.MaxPayloadBytes, event.MaxPayloadBytes + 1} {
			t.Run(fmt.Sprintf("transactional=%t/bytes=%d", transactional, size), func(t *testing.T) {
				reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
				_, mc, inst := newEventValidationCaller(t, primaryDB, reg)
				payload := mustMarshalPayload(t, map[string]any{"data": strings.Repeat("x", size)})
				overhead := len(payload) - size
				payload = mustMarshalPayload(t, map[string]any{"data": strings.Repeat("x", size-overhead)})
				if len(payload) != size {
					t.Fatalf("payload size = %d, want %d", len(payload), size)
				}

				var env abiv1.Envelope
				var tx *sql.Tx
				if transactional {
					txID, opened := beginAndRegisterTx(t, t.Context(), primaryDB, mc)
					tx = opened
					t.Cleanup(func() {
						_ = tx.Rollback()
						mc.RemoveTransaction(txID)
					})
					env = callHost(t, t.Context(), inst, "call_emit_tx", abiv1.EventEmitTxInput{
						TxID: txID, Name: "sales.order.confirmed", Payload: payload,
					})
				} else {
					env = callHost(t, t.Context(), inst, "call_emit", abiv1.EventEmitInput{
						Name: "sales.order.confirmed", Payload: payload,
					})
				}

				if size > event.MaxPayloadBytes {
					if env.OK || env.Error.Code != abiv1.ErrCodeEventPayloadTooLarge || env.Error.Retry {
						t.Fatalf("oversized emission = %+v", env)
					}

					if tx != nil {
						var count int
						if err := tx.QueryRowContext(t.Context(), `SELECT count(*) FROM system.river_job WHERE args->>'tenant_id' = $1`, mc.TenantID).Scan(&count); err != nil || count != 0 {
							t.Fatalf("rejected transaction jobs = %d, error = %v", count, err)
						}

						if err := tx.Rollback(); err != nil {
							t.Fatalf("rollback rejected emission: %v", err)
						}
					}
				} else {
					if !env.OK {
						t.Fatalf("valid emission failed: %+v", env.Error)
					}

					if tx != nil {
						if got := countEventDeliveryJobs(t, primaryDB, mc.TenantID); got != 0 {
							t.Fatalf("uncommitted jobs = %d, want 0", got)
						}

						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
					}
				}

				want := 1
				if size > event.MaxPayloadBytes {
					want = 0
				}

				if got := countEventDeliveryJobs(t, primaryDB, mc.TenantID); got != want {
					t.Fatalf("job count = %d, want %d", got, want)
				}
			})
		}
	}
}

func TestHostEvent_Emit_RejectsOversizedBeforeSyncDispatch(t *testing.T) {
	primaryDB := newEventTestDB(t)
	reg := newEmitterAndSubscriberRegistry("testmodule", "sales.order.confirmed",
		manifest.EventSubscription{Name: "sales.order.confirmed", Handler: "handle_confirmed"},
	)
	r, mc, inst := newEventValidationCaller(t, primaryDB, reg)
	dispatcher := &fakeSyncEventDispatcher{}
	r.SetSyncEventDispatcher(dispatcher)

	env := callHost(t, t.Context(), inst, "call_emit", abiv1.EventEmitInput{
		Name: "sales.order.confirmed", Sync: true, Payload: make([]byte, event.MaxPayloadBytes+1),
	})
	if env.OK || env.Error.Code != abiv1.ErrCodeEventPayloadTooLarge {
		t.Fatalf("oversized emission = %+v", env)
	}

	if len(dispatcher.calls) != 0 || countEventDeliveryJobs(t, primaryDB, mc.TenantID) != 0 {
		t.Fatal("rejected emission dispatched subscribers or inserted a job")
	}
}

func TestHostEvent_DeclaredVersion(t *testing.T) {
	primaryDB := newEventTestDB(t)
	for _, transactional := range []bool{false, true} {
		for _, version := range []int{0, 1, 2, 3} {
			t.Run(fmt.Sprintf("transactional=%t/version=%d", transactional, version), func(t *testing.T) {
				reg := event.NewEventRegistry()
				reg.Register("testmodule", manifest.Manifest{
					Emits: []manifest.EventDeclaration{{Name: "sales.order.confirmed", Version: 2}},
				})
				reg.Register("old_subscriber", manifest.Manifest{
					Subscribes: []manifest.EventSubscription{{Name: "sales.order.confirmed", Version: 1, Handler: "handle_old"}},
				})
				r, mc, inst := newEventValidationCaller(t, primaryDB, reg)
				dispatcher := &fakeSyncEventDispatcher{}
				r.SetSyncEventDispatcher(dispatcher)

				var env abiv1.Envelope
				if transactional {
					txID, tx := beginAndRegisterTx(t, t.Context(), primaryDB, mc)
					t.Cleanup(func() {
						_ = tx.Rollback()
						mc.RemoveTransaction(txID)
					})
					env = callHost(t, t.Context(), inst, "call_emit_tx", abiv1.EventEmitTxInput{
						TxID: txID, Name: "sales.order.confirmed", Version: version,
					})
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				} else {
					env = callHost(t, t.Context(), inst, "call_emit", abiv1.EventEmitInput{
						Name: "sales.order.confirmed", Version: version, Sync: true,
					})
				}

				want := 0
				if version == 2 {
					want = 1
					if !env.OK {
						t.Fatalf("declared emission failed: %+v", env.Error)
					}
				} else if env.OK || env.Error.Code != abiv1.ErrCodeUndeclared || env.Error.Retry {
					t.Fatalf("undeclared emission = %+v", env)
				}

				if len(dispatcher.calls) != 0 {
					t.Fatalf("removed-version subscriber invoked: %v", dispatcher.calls)
				}

				if got := countEventDeliveryJobs(t, primaryDB, mc.TenantID); got != want {
					t.Fatalf("job count = %d, want %d", got, want)
				}
			})
		}
	}
}
