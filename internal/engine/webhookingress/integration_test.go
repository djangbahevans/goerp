package webhookingress

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/connectoringress"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/vmihailenco/msgpack/v5"
)

const integrationPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

// integration wires a real endpoint and inbox store, tenant store, River job
// insert and compiled verifier module behind the handler. Only the tenant
// config (a map) and Redis (absent) are stand-ins.
type integration struct {
	conn      *sql.DB
	store     *connectoringress.Store
	handler   *Handler
	tenantID  string
	token     string
	secrets   *fakeConfig
	connector Connector
	// drainPool drains the connector's pool once, as disabling the module does.
	drainPool func()
}

func newIntegration(t *testing.T) *integration {
	t.Helper()
	ctx := t.Context()

	conn, err := db.New(integrationPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", integrationPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenants := tenant.NewStore(conn)
	if err := tenants.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	store := connectoringress.NewStore(conn)
	if err := store.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}
	tt, err := tenants.CreateTenant(ctx, fmt.Sprintf("ingress%d", time.Now().UnixNano()), "Ingress Integration Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.river_job WHERE kind = 'wasm_job' AND args->>'tenant_id' = $1`, tt.ID)
		_, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID)
	})
	token, err := store.MintEndpoint(ctx, tt.ID, testModule)
	if err != nil {
		t.Fatalf("MintEndpoint() error: %v", err)
	}

	rt, err := wasm.New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 8 << 20,
	}, conn, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(t.Context()) })

	wasmPath := filepath.Join(t.TempDir(), "verifier.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "../wasm/testdata/webhookverifierfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile verifier fixture: %v\n%s", err, out)
	}
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read verifier fixture: %v", err)
	}
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(t.Context()) })
	pool := rt.NewPool(testModule, compiled, wasm.PoolConfig{MaxSize: 2, BorrowTimeout: time.Second})
	drainPool := sync.OnceFunc(func() { pool.DrainAndClose(context.Background(), time.Second) })
	t.Cleanup(drainPool)

	secrets := &fakeConfig{values: map[string]string{testModule + ".webhook_secret": "current"}, encrypted: map[string]bool{}}
	connector := Connector{
		Name: testModule, MediaType: "application/json", HasVerifier: true,
		InboxJob: &manifest.JobType{Name: "paystack_process_inbox", Queue: "default"},
		Source:   wasm.WebhookVerifierSource{ModuleName: testModule, Pool: pool, Compiled: compiled},
	}
	in := &integration{conn: conn, store: store, tenantID: tt.ID, token: token, secrets: secrets, connector: connector, drainPool: drainPool}
	in.handler = NewHandler(Deps{
		Connector: func(name string) (Connector, bool, bool) { return in.connector, true, name == testModule },
		Endpoints: store,
		Inbox:     store,
		Tenants:   tenants,
		Config:    secrets,
		Decrypt:   func(b []byte) ([]byte, error) { return b, nil },
		Verifier:  rt,
		Enqueuer:  rt,
	})
	return in
}

// deliver posts body signed with secret as the fixture's hmac verifier expects.
func (in *integration) deliver(token, secret, eventID, body string) *httptest.ResponseRecorder {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))

	req := httptest.NewRequest(http.MethodPost, "/_webhooks/"+testModule+"/"+token, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Mode", "hmac")
	req.Header.Set("X-Event-Id", eventID)
	req.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	req = req.WithContext(route.WithParams(req.Context(), map[string]string{"module_name": testModule, "token": token}))
	rec := httptest.NewRecorder()
	in.handler.ServeHTTP(rec, req)
	return rec
}

func (in *integration) counts(t *testing.T) (inbox, jobs int) {
	t.Helper()
	if err := in.conn.QueryRowContext(t.Context(),
		`SELECT count(*) FROM system.connector_inbox WHERE tenant_id = $1`, in.tenantID).Scan(&inbox); err != nil {
		t.Fatalf("count inbox rows: %v", err)
	}
	if err := in.conn.QueryRowContext(t.Context(),
		`SELECT count(*) FROM system.river_job WHERE kind = 'wasm_job' AND args->>'tenant_id' = $1 AND args->>'job_type' = 'paystack_process_inbox'`, in.tenantID).Scan(&jobs); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	return inbox, jobs
}

func TestIntegration_SignedDeliveryIsStoredAndItsJobEnqueued(t *testing.T) {
	in := newIntegration(t)
	body := `{"event":"charge.success","data":{"reference":"ref_42","amount":5000}}`

	rec := in.deliver(in.token, "current", "evt_42", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if inbox, jobs := in.counts(t); inbox != 1 || jobs != 1 {
		t.Fatalf("%d inbox rows and %d jobs, want 1 and 1", inbox, jobs)
	}

	var inboxID, status, stored string
	if err := in.conn.QueryRowContext(t.Context(),
		`SELECT id, status, payload::text FROM system.connector_inbox WHERE tenant_id = $1 AND provider_event_id = 'evt_42'`, in.tenantID).Scan(&inboxID, &status, &stored); err != nil {
		t.Fatalf("read inbox row: %v", err)
	}
	if status != "pending" || !strings.Contains(stored, "ref_42") {
		t.Errorf("inbox row status %q payload %s, want pending with the body", status, stored)
	}

	var jobModule string
	if err := in.conn.QueryRowContext(t.Context(),
		`SELECT args->>'module_name' FROM system.river_job WHERE kind = 'wasm_job' AND args->>'tenant_id' = $1`, in.tenantID).Scan(&jobModule); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if jobModule != testModule {
		t.Errorf("job module = %q, want %s", jobModule, testModule)
	}
	var jobPayload struct {
		TenantID string `msgpack:"tenant_id"`
		InboxID  string `msgpack:"inbox_id"`
	}
	var rawPayload []byte
	if err := in.conn.QueryRowContext(t.Context(),
		`SELECT decode(args->>'payload', 'base64') FROM system.river_job WHERE kind = 'wasm_job' AND args->>'tenant_id' = $1`, in.tenantID).Scan(&rawPayload); err != nil {
		t.Fatalf("read job payload: %v", err)
	}
	if err := msgpack.Unmarshal(rawPayload, &jobPayload); err != nil || jobPayload.TenantID != in.tenantID || jobPayload.InboxID != inboxID {
		t.Errorf("job payload = %+v (%v), want {tenant_id, inbox_id=%s}", jobPayload, err, inboxID)
	}
}

func TestIntegration_RedeliveryStoresAndEnqueuesNothingMore(t *testing.T) {
	in := newIntegration(t)
	body := `{"data":{"reference":"ref_1"}}`

	for range 3 {
		if rec := in.deliver(in.token, "current", "evt_dup", body); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
		}
	}

	if inbox, jobs := in.counts(t); inbox != 1 || jobs != 1 {
		t.Errorf("%d inbox rows and %d jobs after three deliveries of one event, want 1 and 1", inbox, jobs)
	}
}

func TestIntegration_EitherSecretVerifiesAndAnotherIs401(t *testing.T) {
	in := newIntegration(t)
	in.secrets.values[testModule+".webhook_secret_previous"] = "previous"

	for _, tt := range []struct {
		name, secret, event string
		want                int
	}{
		{"current secret", "current", "evt_a", http.StatusOK},
		{"previous secret during rotation", "previous", "evt_b", http.StatusOK},
		{"neither secret", "attacker", "evt_c", http.StatusUnauthorized},
	} {
		if rec := in.deliver(in.token, tt.secret, tt.event, `{"n":1}`); rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d", tt.name, rec.Code, tt.want)
		}
	}

	if inbox, jobs := in.counts(t); inbox != 2 || jobs != 2 {
		t.Errorf("%d inbox rows and %d jobs, want 2 and 2: the unauthenticated delivery must leave no trace", inbox, jobs)
	}
}

func TestIntegration_RevokedTokenIs404AndStoresNothing(t *testing.T) {
	in := newIntegration(t)
	if err := in.store.RevokeEndpoint(t.Context(), in.tenantID, testModule); err != nil {
		t.Fatalf("RevokeEndpoint() error: %v", err)
	}

	rec := in.deliver(in.token, "current", "evt_r", `{}`)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if inbox, jobs := in.counts(t); inbox != 0 || jobs != 0 {
		t.Errorf("%d inbox rows and %d jobs after a revoked-token delivery, want none", inbox, jobs)
	}
}

func TestIntegration_ADisabledConnectorStillVerifiesAndStores(t *testing.T) {
	in := newIntegration(t)
	in.drainPool()

	rec := in.deliver(in.token, "current", "evt_d", `{"data":{"reference":"ref_d"}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 from the transient instance: %s", rec.Code, rec.Body)
	}
	if inbox, jobs := in.counts(t); inbox != 1 || jobs != 1 {
		t.Errorf("%d inbox rows and %d jobs, want 1 and 1", inbox, jobs)
	}
}
