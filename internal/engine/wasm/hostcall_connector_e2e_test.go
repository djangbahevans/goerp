package wasm

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/connectoringress"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/vmihailenco/msgpack/v5"
)

const connectorTestPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

// connectorInboxResult and connectorInboxRequest mirror
// testdata/connectorinboxfixture's envelopes.
type connectorInboxRequest struct {
	InboxID string `msgpack:"inbox_id"`
	Reason  string `msgpack:"reason,omitempty"`
}

type connectorInboxResult struct {
	ErrCode    string `msgpack:"err_code,omitempty"`
	Error      string `msgpack:"error,omitempty"`
	ID         string `msgpack:"id,omitempty"`
	EventID    string `msgpack:"event_id,omitempty"`
	Status     string `msgpack:"status,omitempty"`
	ReceivedAt int64  `msgpack:"received_at,omitempty"`
	Body       struct {
		Reference string         `msgpack:"reference"`
		Amount    int64          `msgpack:"amount"`
		Fraction  float64        `msgpack:"fraction"`
		Paid      bool           `msgpack:"paid"`
		Tags      []string       `msgpack:"tags"`
		Meta      map[string]any `msgpack:"meta"`
	} `msgpack:"body"`
}

type connectorInboxEnv struct {
	conn  *sql.DB
	store *connectoringress.Store
	// call invokes a fixture export as tenantID's module moduleName.
	call func(export, tenantID, moduleName string, req connectorInboxRequest) connectorInboxResult
}

func newConnectorInboxEnv(t *testing.T, wireStore bool) *connectorInboxEnv {
	t.Helper()
	ctx := t.Context()

	conn, err := db.New(connectorTestPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", connectorTestPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := tenant.NewStore(conn).Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	store := connectoringress.NewStore(conn)
	if err := store.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	rt, err := New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 8 << 20,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(t.Context()) })
	if wireStore {
		rt.SetConnectorInbox(store)
	}

	compiled, err := rt.wazero.CompileModule(ctx, compileTestdata(t, "connectorinboxfixture"))
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(t.Context()) })
	inst, err := newModuleInstance(ctx, fmt.Sprintf("connectorinboxfixture-%d", time.Now().UnixNano()), compiled, rt.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	rt.RegisterInstance(inst)
	t.Cleanup(func() { rt.UnregisterInstance(inst) })

	call := func(export, tenantID, moduleName string, req connectorInboxRequest) connectorInboxResult {
		t.Helper()
		inst.SetModuleContext(NewModuleContext("req-1", moduleName, "", "", nil, nil, tenantID, "slug", "trace-1", 0, nil, ModuleSnapshot{}))

		payload, err := msgpack.Marshal(req)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		alloc, err := inst.module.ExportedFunction("allocate").Call(ctx, uint64(len(payload)))
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		if !inst.module.Memory().Write(uint32(alloc[0]), payload) {
			t.Fatal("write request: out of bounds")
		}
		results, err := inst.module.ExportedFunction(export).Call(ctx, alloc[0], uint64(len(payload)))
		if err != nil {
			t.Fatalf("call %s: %v", export, err)
		}
		raw, ok := inst.module.Memory().Read(uint32(results[0]>>32), uint32(results[0]))
		if !ok {
			t.Fatalf("read %s result: out of bounds", export)
		}
		var out connectorInboxResult
		if err := msgpack.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal %s result: %v", export, err)
		}
		return out
	}
	return &connectorInboxEnv{conn: conn, store: store, call: call}
}

func (e *connectorInboxEnv) createTenant(t *testing.T) string {
	t.Helper()
	tt, err := tenant.NewStore(e.conn).CreateTenant(t.Context(), fmt.Sprintf("hostconn%d", time.Now().UnixNano()), "Host Connector Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })
	return tt.ID
}

func TestHostConnector_InboxGetReturnsTheAcceptedBodyAsMsgpack(t *testing.T) {
	env := newConnectorInboxEnv(t, true)
	tenantID := env.createTenant(t)
	payload := `{"reference":"ref_1","amount":9007199254740993,"fraction":0.25,"paid":true,"tags":["a","b"],"meta":{"k":null,"n":{"deep":1}}}`
	id, _, err := env.store.InsertInbox(t.Context(), tenantID, "connector_paystack", "evt_1", []byte(payload))
	if err != nil {
		t.Fatalf("InsertInbox() error: %v", err)
	}

	got := env.call("run_get", tenantID, "connector_paystack", connectorInboxRequest{InboxID: id})

	if got.Error != "" {
		t.Fatalf("inbox_get failed: %s %s", got.ErrCode, got.Error)
	}
	if got.ID != id || got.EventID != "evt_1" || got.Status != "pending" || got.ReceivedAt == 0 {
		t.Errorf("row = %+v, want id %s, event evt_1, pending, a timestamp", got, id)
	}
	if got.Body.Reference != "ref_1" || got.Body.Amount != 9007199254740993 || got.Body.Fraction != 0.25 || !got.Body.Paid {
		t.Errorf("body = %+v, want the exact integer 9007199254740993 and the other scalar fields", got.Body)
	}
	if len(got.Body.Tags) != 2 || got.Body.Tags[1] != "b" {
		t.Errorf("tags = %v, want [a b]", got.Body.Tags)
	}
	if v, present := got.Body.Meta["k"]; !present || v != nil {
		t.Errorf("meta[k] = %v present=%v, want a present null", v, present)
	}
}

func TestHostConnector_MarkProcessedAndFailed(t *testing.T) {
	env := newConnectorInboxEnv(t, true)
	tenantID := env.createTenant(t)
	id, _, err := env.store.InsertInbox(t.Context(), tenantID, "connector_paystack", "evt_1", []byte(`{}`))
	if err != nil {
		t.Fatalf("InsertInbox() error: %v", err)
	}

	if got := env.call("run_mark_failed", tenantID, "connector_paystack", connectorInboxRequest{InboxID: id, Reason: "invalid payload"}); got.Error != "" {
		t.Fatalf("inbox_mark_failed: %s", got.Error)
	}
	if got := env.call("run_get", tenantID, "connector_paystack", connectorInboxRequest{InboxID: id}); got.Status != "failed" {
		t.Errorf("status after mark_failed = %q, want failed", got.Status)
	}

	for range 2 {
		if got := env.call("run_mark_processed", tenantID, "connector_paystack", connectorInboxRequest{InboxID: id}); got.Error != "" {
			t.Fatalf("inbox_mark_processed: %s", got.Error)
		}
	}
	if got := env.call("run_get", tenantID, "connector_paystack", connectorInboxRequest{InboxID: id}); got.Status != "processed" {
		t.Errorf("status after mark_processed = %q, want processed", got.Status)
	}
}

func TestHostConnector_RowsOfAnotherTenantOrModuleAreNotFound(t *testing.T) {
	env := newConnectorInboxEnv(t, true)
	tenantID, otherTenantID := env.createTenant(t), env.createTenant(t)
	id, _, err := env.store.InsertInbox(t.Context(), tenantID, "connector_paystack", "evt_1", []byte(`{}`))
	if err != nil {
		t.Fatalf("InsertInbox() error: %v", err)
	}

	for _, tt := range []struct{ name, tenant, module, id string }{
		{"another tenant", otherTenantID, "connector_paystack", id},
		{"another module", tenantID, "connector_stripe", id},
		{"unknown id", tenantID, "connector_paystack", "00000000-0000-7000-8000-000000000000"},
		{"not a uuid", tenantID, "connector_paystack", "not-a-uuid"},
	} {
		for _, export := range []string{"run_get", "run_mark_processed", "run_mark_failed"} {
			got := env.call(export, tt.tenant, tt.module, connectorInboxRequest{InboxID: tt.id, Reason: "x"})
			if got.ErrCode != abiv1.ErrCodeConnectorInboxNotFound {
				t.Errorf("%s %s: error code = %q (%s), want %s", tt.name, export, got.ErrCode, got.Error, abiv1.ErrCodeConnectorInboxNotFound)
			}
		}
	}

	if got := env.call("run_get", tenantID, "connector_paystack", connectorInboxRequest{InboxID: id}); got.Status != "pending" {
		t.Errorf("row status = %q after rejected writes, want it untouched (pending)", got.Status)
	}
}

func TestHostConnector_WithoutAStoreIsUnavailable(t *testing.T) {
	env := newConnectorInboxEnv(t, false)

	got := env.call("run_get", "tenant-1", "connector_paystack", connectorInboxRequest{InboxID: "x"})
	if got.ErrCode != abiv1.ErrCodeUnavailable {
		t.Errorf("error code = %q, want %s", got.ErrCode, abiv1.ErrCodeUnavailable)
	}
}
