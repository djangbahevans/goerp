package eventdelivery

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

type transactionalTenantResolver struct{ tenant *tenant.Tenant }

func (r transactionalTenantResolver) GetByID(context.Context, string) (*tenant.Tenant, error) {
	return r.tenant, nil
}

type transactionalFixture struct {
	admin      *sql.DB
	worker     *SubscriberDeliveryWorker
	runtime    *wasm.Runtime
	tenant     *tenant.Tenant
	schema     string
	engineRole string
}

func newTransactionalFixture(t *testing.T) *transactionalFixture {
	t.Helper()
	ctx := t.Context()
	cleanupCtx := context.WithoutCancel(ctx)

	admin, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("Postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	slug := "eventtx" + strings.ReplaceAll(uuid.New().String(), "-", "")
	schema := tenantschema.Name(slug)
	engineRole := "eventengine_" + slug
	tn := &tenant.Tenant{ID: uuid.New().String(), Slug: slug}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tenantschema.Drop(cleanupCtx, admin, slug); err != nil {
			t.Errorf("drop transaction fixture: %v", err)
		}
	})

	for _, group := range enginetables.Groups {
		if group.Tables[0].Name == enginetables.EventDeliveriesTable {
			if err := group.Create(ctx, admin, slug); err != nil {
				t.Fatal(err)
			}
		}
	}

	if _, err := admin.ExecContext(ctx, `CREATE TABLE `+schema+`.effects (
		id UUID PRIMARY KEY DEFAULT uuidv7(),
		event_id TEXT NOT NULL, tx_id TEXT NOT NULL, mode TEXT NOT NULL, role_name TEXT NOT NULL, user_id TEXT NOT NULL
	); CREATE TABLE `+schema+`.reference_target (id INT PRIMARY KEY);
	CREATE TABLE `+schema+`.pending_reference (
		ref_id INT REFERENCES `+schema+`.reference_target(id) DEFERRABLE INITIALLY DEFERRED
	)`); err != nil {
		t.Fatal(err)
	}

	// Both roles belong to this fixture; shared engine memberships stay untouched.
	if _, err := admin.ExecContext(ctx, "CREATE ROLE "+schema+"; CREATE ROLE "+engineRole+" LOGIN PASSWORD 'dev' NOINHERIT; GRANT "+schema+" TO "+engineRole); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(cleanupCtx, "DROP OWNED BY "+engineRole+", "+schema+"; DROP ROLE "+engineRole+", "+schema); err != nil {
			t.Errorf("drop transaction fixture roles: %v", err)
		}
	})
	if _, err := admin.ExecContext(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+engineRole+", "+schema+
		"; GRANT SELECT, INSERT, UPDATE ON "+schema+".event_deliveries TO "+engineRole+
		"; GRANT SELECT ON "+schema+".effects TO "+engineRole+", "+schema+
		"; GRANT INSERT ON "+schema+".effects, "+schema+".pending_reference TO "+schema+
		"; GRANT SELECT ON "+schema+".reference_target TO "+schema); err != nil {
		t.Fatal(err)
	}

	primary, err := db.New("postgres://" + engineRole + ":dev@localhost:15432/goerp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = primary.Close() })

	rt, err := wasm.New(&config.Config{
		CompilationCache:            wasmtest.SharedCompilationCacheDir(),
		PoolMaxMemoryByes:           64 << 20,
		Environment:                 string(config.Production),
		DBMaxConcurrentTransactions: 2,
	}, primary, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(cleanupCtx) })

	path := filepath.Join(t.TempDir(), "transactionfixture.wasm")
	cmd := exec.CommandContext(ctx, "go", "build", "-buildmode=c-shared", "-o", path, "./testdata/transactionfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build transaction fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	compiled, err := rt.CompileModule(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(cleanupCtx) })

	pool := rt.NewPool("transactionfixture", compiled, wasm.PoolConfig{MaxSize: 2, BorrowTimeout: 5 * time.Second})
	t.Cleanup(func() { pool.DrainAndClose(cleanupCtx, 5*time.Second) })
	effects := model.Define("effect", model.Table("effects")).
		Field("id", model.UUID().PrimaryKey()).
		Field("event_id", model.Text()).
		Field("tx_id", model.Text())

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"transactionfixture": {
			Status:       module.StatusReady,
			Pool:         pool,
			Capabilities: abi.CapDBRead | abi.CapDBWrite,
			ModelDecls:   []model.ModelDeclaration{*effects},
			Manifest: manifest.Manifest{
				Name: "transactionfixture",
				Type: "standard",
				Subscribes: []manifest.EventSubscription{
					{Name: testEventName, Handler: "handleTransactionalEvent", Async: true, Transactional: true},
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	return &transactionalFixture{
		admin:      admin,
		runtime:    rt,
		tenant:     tn,
		schema:     schema,
		engineRole: engineRole,
		worker: &SubscriberDeliveryWorker{
			ModuleRegistry: reg,
			Invoker:        &HandlerInvoker{Runtime: rt, TenantStore: transactionalTenantResolver{tenant: tn}},
		},
	}
}

func (f *transactionalFixture) args(t *testing.T, mode string) jobqueue.SubscriberDeliveryArgs {
	t.Helper()
	payload, err := msgpack.Marshal(mode)
	if err != nil {
		t.Fatal(err)
	}

	return jobqueue.SubscriberDeliveryArgs{
		EventID:       uuid.New().String(),
		EventName:     testEventName,
		EventVersion:  1,
		EmitterModule: "emitter",
		TenantID:      f.tenant.ID,
		EmittedAt:     time.Now().UTC(),
		ModuleName:    "transactionfixture",
		HandlerName:   "handleTransactionalEvent",
		Payload:       payload,
	}
}

func (f *transactionalFixture) work(ctx context.Context, args jobqueue.SubscriberDeliveryArgs) error {
	return f.worker.Work(ctx, &river.Job[jobqueue.SubscriberDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args})
}

func (f *transactionalFixture) assertRows(t *testing.T, eventID string, effects, ledger int) {
	t.Helper()
	var gotEffects, gotLedger int
	if err := f.admin.QueryRowContext(t.Context(), "SELECT count(*) FROM "+f.schema+".effects WHERE event_id = $1", eventID).Scan(&gotEffects); err != nil {
		t.Fatal(err)
	}
	if err := f.admin.QueryRowContext(t.Context(), "SELECT count(*) FROM "+f.schema+".event_deliveries WHERE event_id = $1", eventID).Scan(&gotLedger); err != nil {
		t.Fatal(err)
	}

	if gotEffects != effects || gotLedger != ledger {
		t.Fatalf("effects=%d ledger=%d, want effects=%d ledger=%d", gotEffects, gotLedger, effects, ledger)
	}
	limiter := f.runtime.TxLimiter()
	for range 2 {
		if !limiter.TryAcquire() {
			t.Fatal("delivery leaked a transaction limiter slot")
		}
	}
	limiter.Release()
	limiter.Release()
}

func TestSubscriberDeliveryWorker_TransactionalCommitAndReplay(t *testing.T) {
	f := newTransactionalFixture(t)
	args := f.args(t, "ownership")
	for range 2 {
		if err := f.work(t.Context(), args); err != nil {
			t.Fatal(err)
		}
	}
	f.assertRows(t, args.EventID, 1, 1)

	var roleName, userID, txID string
	if err := f.admin.QueryRowContext(t.Context(), "SELECT role_name, user_id, tx_id FROM "+f.schema+".effects WHERE event_id = $1", args.EventID).Scan(&roleName, &userID, &txID); err != nil {
		t.Fatal(err)
	}
	if roleName != "tenant_"+f.tenant.Slug || userID != "" || txID == "" {
		t.Fatalf("transaction scope: role=%q user=%q tx=%q", roleName, userID, txID)
	}

	oldDelivery := time.Now().Add(-time.Hour)
	if _, err := f.admin.ExecContext(t.Context(), "UPDATE "+f.schema+".event_deliveries SET delivered_at = $1 WHERE event_id = $2", oldDelivery, args.EventID); err != nil {
		t.Fatal(err)
	}
	args.Replay = true
	if err := f.work(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	f.assertRows(t, args.EventID, 2, 1)

	var deliveredAt time.Time
	if err := f.admin.QueryRowContext(t.Context(), "SELECT delivered_at FROM "+f.schema+".event_deliveries WHERE event_id = $1", args.EventID).Scan(&deliveredAt); err != nil {
		t.Fatal(err)
	}
	if !deliveredAt.After(oldDelivery) {
		t.Fatal("replay did not refresh the delivery timestamp")
	}

	args.Payload, _ = msgpack.Marshal("retry")
	if err := f.work(t.Context(), args); err == nil {
		t.Fatal("failed replay returned success")
	}
	f.assertRows(t, args.EventID, 2, 1)
	var afterFailure time.Time
	if err := f.admin.QueryRowContext(t.Context(), "SELECT delivered_at FROM "+f.schema+".event_deliveries WHERE event_id = $1", args.EventID).Scan(&afterFailure); err != nil {
		t.Fatal(err)
	}
	if !afterFailure.Equal(deliveredAt) {
		t.Fatal("failed replay committed its ledger update")
	}
}

func TestSubscriberDeliveryWorker_TransactionalFailureRollsBack(t *testing.T) {
	f := newTransactionalFixture(t)
	for _, mode := range []string{"retry", "permanent", "trap", "commit_failure"} {
		t.Run(mode, func(t *testing.T) {
			args := f.args(t, mode)
			err := f.work(t.Context(), args)
			if err == nil {
				t.Fatal("failed handler returned success")
			}
			if _, canceled := errors.AsType[*river.JobCancelError](err); mode == "permanent" && !canceled {
				t.Fatalf("permanent status must cancel the job: %v", err)
			}
			if mode == "commit_failure" && !strings.Contains(err.Error(), "commit event delivery") {
				t.Fatalf("expected failure at commit: %v", err)
			}
			f.assertRows(t, args.EventID, 0, 0)

			args.Payload, _ = msgpack.Marshal("success")
			if err := f.work(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			f.assertRows(t, args.EventID, 1, 1)
		})
	}
}

func (f *transactionalFixture) waitForBlockedQuery(t *testing.T, fragment string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticks := time.Tick(10 * time.Millisecond)

	for {
		var blocked bool
		err := f.admin.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE $1 AND usename = $2
		)`, "%"+fragment+"%", f.engineRole).Scan(&blocked)
		if err != nil {
			t.Fatalf("wait for blocked %q: %v", fragment, err)
		}
		if blocked {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("query containing %q did not block", fragment)
		case <-ticks:
		}
	}
}

func TestSubscriberDeliveryWorker_TransactionalConcurrentDuplicates(t *testing.T) {
	f := newTransactionalFixture(t)
	for _, firstMode := range []string{"success", "retry"} {
		t.Run(firstMode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			args := f.args(t, firstMode)

			blocker, err := f.admin.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback() }()
			if _, err := blocker.ExecContext(ctx, "LOCK TABLE "+f.schema+".effects IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}

			first, second := make(chan error, 1), make(chan error, 1)
			var wg sync.WaitGroup
			defer func() {
				_ = blocker.Rollback()
				cancel()
				wg.Wait()
			}()
			wg.Go(func() { first <- f.work(ctx, args) })
			f.waitForBlockedQuery(t, "INSERT INTO effects")

			duplicate := args
			duplicate.Payload, _ = msgpack.Marshal("success")
			wg.Go(func() { second <- f.work(ctx, duplicate) })
			f.waitForBlockedQuery(t, "INSERT INTO "+f.schema+".event_deliveries")

			if err := blocker.Commit(); err != nil {
				t.Fatal(err)
			}
			wg.Wait()
			if err := <-first; (err != nil) != (firstMode == "retry") {
				t.Fatalf("first delivery in mode %s: %v", firstMode, err)
			}
			if err := <-second; err != nil {
				t.Fatalf("second delivery: %v", err)
			}
			f.assertRows(t, args.EventID, 1, 1)
		})
	}
}

func TestSubscriberDeliveryWorker_TransactionalCancellation(t *testing.T) {
	f := newTransactionalFixture(t)
	args := f.args(t, "success")
	blocker, err := f.admin.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, err := blocker.ExecContext(t.Context(), "LOCK TABLE "+f.schema+".effects IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() { result <- f.work(ctx, args) })
	defer func() {
		_ = blocker.Rollback()
		cancel()
		wg.Wait()
	}()
	f.waitForBlockedQuery(t, "INSERT INTO effects")
	cancel()
	wg.Wait()
	if err := <-result; err == nil {
		t.Fatal("canceled delivery returned success")
	}

	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	f.assertRows(t, args.EventID, 0, 0)

	if err := f.work(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	f.assertRows(t, args.EventID, 1, 1)
}
