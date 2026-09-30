package notify

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/jobdispatch"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	smsConnector  = "connector_smsfixture"
	pushConnector = "connector_pushfixture"
)

// sendParked is Send, with every job it enqueues parked an hour ahead in
// the same transaction: another package's started River client polling
// the shared queues can never claim one, and the tests work them
// directly.
func (e *testEnv) sendParked(t *testing.T, userID string, data map[string]any, opts Options) *Result {
	t.Helper()
	tx, err := e.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := e.sender.SendTx(t.Context(), tx, e.tenant.ID, "sales", orderConfirmed, userID, data, opts)
	if err != nil {
		t.Fatalf("SendTx() error: %v", err)
	}
	if _, err := tx.Exec(`
		UPDATE system.river_job SET state = 'scheduled', scheduled_at = NOW() + interval '1 hour'
		WHERE args->>'tenant_id' = $1 AND state = 'available'
	`, e.tenant.ID); err != nil {
		t.Fatalf("park jobs: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	e.sender.Announce(t.Context(), res)
	return res
}

// providerJob loads the jobType job the pipeline enqueued for env's
// tenant, as a River job at attempt of maxAttempts.
func (e *testEnv) providerJob(t *testing.T, jobType string, attempt, maxAttempts int) *river.Job[jobqueue.WASMJobArgs] {
	t.Helper()
	var raw []byte
	if err := e.conn.QueryRow(`SELECT args FROM system.river_job WHERE kind = 'wasm_job' AND args->>'tenant_id' = $1 AND args->>'job_type' = $2`,
		e.tenant.ID, jobType).Scan(&raw); err != nil {
		t.Fatalf("load %s job: %v", jobType, err)
	}
	var args jobqueue.WASMJobArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	return &river.Job[jobqueue.WASMJobArgs]{JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: maxAttempts}, Args: args}
}

type attemptRow struct {
	recipient, status, reason string
	attempts                  int
}

func (e *testEnv) channelDeliveries(t *testing.T, channel string) []attemptRow {
	t.Helper()
	rows, err := e.conn.Query(fmt.Sprintf(`
		SELECT recipient, status, COALESCE(failure_reason, ''), attempt_count
		FROM %s.notification_deliveries WHERE channel = $1 ORDER BY recipient
	`, tenantschema.Name(e.tenant.Slug)), channel)
	if err != nil {
		t.Fatalf("query %s deliveries: %v", channel, err)
	}
	defer rows.Close()
	var out []attemptRow
	for rows.Next() {
		var r attemptRow
		if err := rows.Scan(&r.recipient, &r.status, &r.reason, &r.attempts); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// setDeliveryStatus stands in for a connector's delivery report on one
// recipient.
func (e *testEnv) setDeliveryStatus(t *testing.T, channel, recipient, status string) {
	t.Helper()
	if _, err := e.conn.Exec(fmt.Sprintf(`UPDATE %s.notification_deliveries SET status = $3 WHERE channel = $1 AND recipient = $2`,
		tenantschema.Name(e.tenant.Slug)), channel, recipient, status); err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) tracker() *ProviderDeliveries {
	return &ProviderDeliveries{DB: e.conn, Tenants: tenant.NewStore(e.conn)}
}

// sendPushToThreeDevices sends orderConfirmed to a new user with three
// registered devices, returning its push_send job at attempt 1 of 5.
func sendPushToThreeDevices(t *testing.T, env *testEnv) *river.Job[jobqueue.WASMJobArgs] {
	t.Helper()
	userID := env.createUser(t, "Ama Owusu", "")
	for _, tok := range []string{"tok-a", "tok-b", "tok-c"} {
		env.registerDevice(t, userID, "android", tok)
	}
	env.sendParked(t, userID, nil, Options{})
	return env.providerJob(t, JobTypePushSend, 1, 5)
}

func pushTokens(t *testing.T, payload []byte) []string {
	t.Helper()
	var p abiv1.PushSendPayload
	if err := msgpack.Unmarshal(payload, &p); err != nil {
		t.Fatalf("decode push_send payload: %v", err)
	}
	var out []string
	for _, tok := range p.Tokens {
		out = append(out, tok.Token)
	}
	return out
}

// Each device token is its own delivery: a token the provider already
// reported on is left out of a retry and keeps its status, while the
// others move on independently.
func TestProviderDeliveries_PushTokensAreTrackedIndependently(t *testing.T) {
	env := openTestEnv(t)
	job := sendPushToThreeDevices(t, env)
	tracker := env.tracker()

	payload, ok, err := tracker.Begin(t.Context(), job.Args)
	if err != nil || !ok {
		t.Fatalf("Begin() = %v, %v", ok, err)
	}
	if !slices.Equal(payload, job.Args.Payload) {
		t.Error("Begin() rewrote the payload although every delivery is open")
	}

	env.setDeliveryStatus(t, push, "tok-b", notifications.DeliveryFailed)
	payload, ok, err = tracker.Begin(t.Context(), job.Args)
	if err != nil || !ok {
		t.Fatalf("Begin() = %v, %v", ok, err)
	}
	if got := pushTokens(t, payload); !slices.Equal(got, []string{"tok-a", "tok-c"}) {
		t.Errorf("Begin() tokens = %v, want tok-a and tok-c", got)
	}

	if err := tracker.Finish(t.Context(), job, nil); err != nil {
		t.Fatalf("Finish() error: %v", err)
	}
	want := []attemptRow{
		{recipient: "tok-a", status: notifications.DeliveryAccepted, attempts: 1},
		{recipient: "tok-b", status: notifications.DeliveryFailed},
		{recipient: "tok-c", status: notifications.DeliveryAccepted, attempts: 1},
	}
	if got := env.channelDeliveries(t, push); !slices.Equal(got, want) {
		t.Errorf("deliveries = %+v\nwant %+v", got, want)
	}

	env.setDeliveryStatus(t, push, "tok-a", notifications.DeliveryDelivered)
	if got := env.channelDeliveries(t, push); got[0].status != notifications.DeliveryDelivered || got[2].status != notifications.DeliveryAccepted {
		t.Errorf("deliveries = %+v, want tok-a delivered and tok-c still accepted", got)
	}

	if _, ok, err := tracker.Begin(t.Context(), job.Args); err != nil || ok {
		t.Errorf("Begin() with every delivery final = %v, %v; want nothing to send", ok, err)
	}
}

func TestProviderDeliveries_FailureRetriesUntilTheLastAttempt(t *testing.T) {
	env := openTestEnv(t)
	job := sendPushToThreeDevices(t, env)
	tracker := env.tracker()
	env.setDeliveryStatus(t, push, "tok-c", notifications.DeliveryQuotaExceeded)

	cause := errors.New("provider unavailable")
	if err := tracker.Finish(t.Context(), job, cause); !errors.Is(err, cause) {
		t.Fatalf("Finish() = %v, want the attempt's own error", err)
	}
	want := []attemptRow{
		{recipient: "tok-a", status: notifications.DeliveryRetrying, reason: cause.Error(), attempts: 1},
		{recipient: "tok-b", status: notifications.DeliveryRetrying, reason: cause.Error(), attempts: 1},
		{recipient: "tok-c", status: notifications.DeliveryQuotaExceeded},
	}
	if got := env.channelDeliveries(t, push); !slices.Equal(got, want) {
		t.Errorf("after attempt 1: deliveries = %+v\nwant %+v", got, want)
	}

	job.Attempt = 5
	if err := tracker.Finish(t.Context(), job, cause); !errors.Is(err, cause) {
		t.Fatalf("Finish() = %v, want the attempt's own error", err)
	}
	for _, d := range env.channelDeliveries(t, push) {
		if d.status != notifications.DeliveryFailed || d.attempts != 5 {
			t.Errorf("after the last attempt: %+v, want failed at attempt 5", d)
		}
	}
}

func TestProviderDeliveries_PermanentFailureFailsAtOnce(t *testing.T) {
	env := openTestEnv(t)
	job := sendPushToThreeDevices(t, env)

	cancel := river.JobCancel(errors.New("invalid recipient"))
	if err := env.tracker().Finish(t.Context(), job, cancel); !errors.Is(err, cancel) {
		t.Fatalf("Finish() = %v, want the cancel", err)
	}
	for _, d := range env.channelDeliveries(t, push) {
		if d.status != notifications.DeliveryFailed || d.attempts != 1 {
			t.Errorf("%+v, want failed at attempt 1", d)
		}
	}
}

// useInstalledProviders resolves env's providers from the real
// tenant_module_settings and tenant_provider_selections tables, with
// modules installed as enabled providers for their category.
func (e *testEnv) useInstalledProviders(t *testing.T, modules map[string]string) {
	t.Helper()
	ctx := t.Context()
	if err := billing.NewStore(e.conn).Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}
	store := providerselect.NewStore(e.conn)
	if err := store.Bootstrap(ctx); err != nil {
		t.Fatalf("providerselect Bootstrap() error: %v", err)
	}
	for name, category := range modules {
		if _, err := e.conn.Exec(`
			INSERT INTO system.tenant_module_settings (tenant_id, module_name, enabled, provider_category)
			VALUES ($1, $2, true, $3)
		`, e.tenant.ID, name, category); err != nil {
			t.Fatalf("install %s: %v", name, err)
		}
	}
	e.sender.Providers = store
}

// A user with a phone number still gets no sms_send when the tenant has
// no SMS connector installed: routing drops the channel.
func TestSend_NoSMSConnectorInstalledEnqueuesNoSMSJob(t *testing.T) {
	env := openTestEnv(t)
	env.useInstalledProviders(t, nil)
	userID := env.createUser(t, "Kofi Mensah", "+233501234567")

	res, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, userID, nil, Options{ForceChannels: []string{sms}})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if slices.Contains(res.ChannelsUsed, sms) {
		t.Errorf("ChannelsUsed = %v, want no sms", res.ChannelsUsed)
	}
	if n := env.count(t, `SELECT count(*) FROM system.river_job WHERE args->>'tenant_id' = $1 AND args->>'job_type' = 'sms_send'`, env.tenant.ID); n != 0 {
		t.Errorf("%d sms_send jobs enqueued, want 0", n)
	}
	if got := env.channelDeliveries(t, sms); len(got) != 0 {
		t.Errorf("sms deliveries = %+v, want none", got)
	}
}

// connectorFixture compiles testdata/connectorfixture once per test binary.
var connectorFixture = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "connectorfixture")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	wasmPath := filepath.Join(dir, "connectorfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/connectorfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, errors.New(string(out))
	}
	return os.ReadFile(wasmPath)
})

func compileConnectorFixture(t *testing.T) []byte {
	t.Helper()
	wasmBytes, err := connectorFixture()
	if err != nil {
		t.Fatalf("compile testdata/connectorfixture: %v", err)
	}
	return wasmBytes
}

// connectorWorker is a real jobdispatch.Worker with testdata/connectorfixture
// loaded as moduleName, a provider for category.
func (e *testEnv) connectorWorker(t *testing.T, moduleName, category string) *jobdispatch.Worker {
	t.Helper()
	rt, err := wasm.New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, e.conn, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	compiled, err := rt.CompileModule(t.Context(), compileConnectorFixture(t))
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
	pool := rt.NewPool(moduleName, compiled, wasm.PoolConfig{MaxSize: 1, BorrowTimeout: 5 * time.Second})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), 5*time.Second) })

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		moduleName: {
			Status: module.StatusReady,
			Pool:   pool,
			Manifest: manifest.Manifest{
				Name:     moduleName,
				Type:     "connector",
				Provides: map[string]bool{category: true},
				JobTypes: []manifest.JobType{{Name: "connectorfixture_observed", Handler: "connectorfixture_observed", Queue: jobqueue.QueueDefault}},
			},
			Capabilities: abi.CapJobsEnqueue,
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}
	tenants := tenant.NewStore(e.conn)
	return &jobdispatch.Worker{ModuleRegistry: reg, Runtime: rt, TenantStore: tenants, Deliveries: &ProviderDeliveries{DB: e.conn, Tenants: tenants}}
}

type connectorObserved struct {
	JobType string                `msgpack:"job_type"`
	Attempt int                   `msgpack:"attempt"`
	SMS     abiv1.SMSSendPayload  `msgpack:"sms"`
	Push    abiv1.PushSendPayload `msgpack:"push"`
}

func (e *testEnv) connectorObservations(t *testing.T) []connectorObserved {
	t.Helper()
	rows, err := e.conn.Query(`SELECT args FROM system.river_job WHERE args->>'tenant_id' = $1 AND args->>'job_type' = 'connectorfixture_observed' ORDER BY id`, e.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []connectorObserved
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var args jobqueue.WASMJobArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
		var obs connectorObserved
		if err := msgpack.Unmarshal(args.Payload, &obs); err != nil {
			t.Fatalf("decode observation: %v", err)
		}
		out = append(out, obs)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The end-to-end path: a send routed to the tenant's installed SMS and
// push connectors enqueues their jobs, a real connector module receives
// well-formed payloads through its own handle_job and engine.OnJob, and
// its success leaves every delivery accepted. A retried job sends nothing
// more.
func TestProviderDelivery_RealConnectorReceivesPayloadsAndAcceptsDeliveries(t *testing.T) {
	env := openTestEnv(t)
	env.useInstalledProviders(t, map[string]string{smsConnector: providerselect.CategorySMS, pushConnector: providerselect.CategoryPush})
	workers := map[string]*jobdispatch.Worker{
		JobTypeSMSSend:  env.connectorWorker(t, smsConnector, providerselect.CategorySMS),
		JobTypePushSend: env.connectorWorker(t, pushConnector, providerselect.CategoryPush),
	}
	userID := env.createUser(t, "Kofi Mensah", "+233501234567")
	env.registerDevice(t, userID, "android", "tok-a")
	env.registerDevice(t, userID, "ios", "tok-b")

	res := env.sendParked(t, userID, map[string]any{"OrderReference": "ORD-7", "OrderID": "o-7"}, Options{ForceChannels: []string{sms}})
	if want := []string{inApp, email, sms, push}; !slices.Equal(res.ChannelsUsed, want) {
		t.Fatalf("ChannelsUsed = %v, want %v", res.ChannelsUsed, want)
	}

	for _, jobType := range []string{JobTypeSMSSend, JobTypePushSend} {
		if err := workers[jobType].Work(t.Context(), env.providerJob(t, jobType, 1, 5)); err != nil {
			t.Fatalf("Work(%s) error: %v", jobType, err)
		}
	}

	obs := env.connectorObservations(t)
	if len(obs) != 2 {
		t.Fatalf("connector saw %d jobs, want 2: %+v", len(obs), obs)
	}
	wantSMS := abiv1.SMSSendPayload{
		SchemaVersion: abiv1.ProviderPayloadSchemaVersion, TenantID: env.tenant.ID, NotificationID: res.NotificationID,
		To: "+233501234567", From: "ACME", Body: "Notify Test Co: order ORD-7 confirmed.",
		IdempotencyKey: res.NotificationID + ":sms:+233501234567",
	}
	if obs[0].JobType != JobTypeSMSSend || obs[0].SMS != wantSMS {
		t.Errorf("sms_send observed = %+v\nwant %+v", obs[0], wantSMS)
	}
	gotPush := obs[1].Push
	if obs[1].JobType != JobTypePushSend || gotPush.Title != "Order ORD-7" || gotPush.Body != "Confirmed by Notify Test Co" ||
		gotPush.ActionURL != "/_m/sales/orders/o-7" || len(gotPush.Tokens) != 2 || gotPush.Tokens[1].Platform != "ios" {
		t.Errorf("push_send observed = %+v", obs[1])
	}

	for _, channel := range []string{sms, push} {
		for _, d := range env.channelDeliveries(t, channel) {
			if d.status != notifications.DeliveryAccepted || d.attempts != 1 {
				t.Errorf("%s delivery %+v, want accepted at attempt 1", channel, d)
			}
		}
	}

	if err := workers[JobTypeSMSSend].Work(t.Context(), env.providerJob(t, JobTypeSMSSend, 2, 5)); err != nil {
		t.Fatalf("Work(retried sms_send) error: %v", err)
	}
	if n := len(env.connectorObservations(t)); n != 2 {
		t.Errorf("a retried sms_send reached the connector again: %d observations, want 2", n)
	}
}

// A connector's plain error leaves its deliveries retrying and reaches
// River for a retry.
func TestProviderDelivery_RealConnectorErrorLeavesDeliveriesRetrying(t *testing.T) {
	env := openTestEnv(t)
	env.useInstalledProviders(t, map[string]string{smsConnector: providerselect.CategorySMS})
	worker := env.connectorWorker(t, smsConnector, providerselect.CategorySMS)
	userID := env.createUser(t, "Kofi Mensah", "+233501234567")

	env.sendParked(t, userID, map[string]any{"OrderReference": "fail"}, Options{ForceChannels: []string{sms}})
	if err := worker.Work(t.Context(), env.providerJob(t, JobTypeSMSSend, 1, 5)); err == nil {
		t.Fatal("Work(sms_send) = nil, want the connector's error so River retries")
	}
	if got := env.channelDeliveries(t, sms); len(got) != 1 || got[0].status != notifications.DeliveryRetrying || got[0].reason == "" {
		t.Errorf("sms deliveries = %+v, want one retrying with a reason", got)
	}
}

// A connector's jobs.PermanentError cancels the job and fails its
// deliveries at once.
func TestProviderDelivery_RealConnectorPermanentErrorFailsDeliveries(t *testing.T) {
	env := openTestEnv(t)
	env.useInstalledProviders(t, map[string]string{pushConnector: providerselect.CategoryPush})
	worker := env.connectorWorker(t, pushConnector, providerselect.CategoryPush)
	userID := env.createUser(t, "Esi", "")
	env.registerDevice(t, userID, "android", "tok-a")

	env.sendParked(t, userID, map[string]any{"OrderReference": "permanent"}, Options{})
	err := worker.Work(t.Context(), env.providerJob(t, JobTypePushSend, 1, 5))
	if _, ok := errors.AsType[*river.JobCancelError](err); !ok {
		t.Fatalf("Work(push_send) = %v, want a JobCancelError", err)
	}
	if got := env.channelDeliveries(t, push); len(got) != 1 || got[0].status != notifications.DeliveryFailed || got[0].attempts != 1 {
		t.Errorf("push deliveries = %+v, want one failed at attempt 1", got)
	}
}

// A type with no sms or push template sends its in_app title and body.
func TestRenderProviderChannels_FallsBackToInAppContent(t *testing.T) {
	plan := []channelPlan{{channel: sms}, {channel: push}}
	got := renderProviderChannels(nil, plan, inAppContent{Title: "Order confirmed", Body: "Total GH₵10"}, nil)
	if got.smsBody != "Order confirmed\nTotal GH₵10" || got.pushTitle != "Order confirmed" || got.pushBody != "Total GH₵10" || len(got.failed) != 0 {
		t.Errorf("renderProviderChannels() = %+v, want the in_app title and body on both channels and no failures", got)
	}
}

// Only the in_app template failing fails a send: an sms template that
// cannot render fails the sms delivery alone, enqueueing nothing for it.
func TestSend_SMSRenderFailureFailsOnlyTheSMSDelivery(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Kofi Mensah", "+233501234567")

	res := env.sendParked(t, userID, map[string]any{"OrderReference": "ORD-7", "BreakSMS": []any{"x"}}, Options{ForceChannels: []string{sms}})
	if want := []string{inApp, email, sms}; !slices.Equal(res.ChannelsUsed, want) {
		t.Errorf("ChannelsUsed = %v, want %v", res.ChannelsUsed, want)
	}
	got := env.channelDeliveries(t, sms)
	if len(got) != 1 || got[0].status != notifications.DeliveryFailed || !strings.Contains(got[0].reason, "could not be rendered") {
		t.Errorf("sms deliveries = %+v, want one failed for the render error", got)
	}
	if n := env.count(t, `SELECT count(*) FROM system.river_job WHERE args->>'tenant_id' = $1 AND args->>'job_type' = 'sms_send'`, env.tenant.ID); n != 0 {
		t.Errorf("%d sms_send jobs enqueued, want 0", n)
	}
}

// A job that cannot even start its attempt still records it: on its last
// attempt, its deliveries are failed rather than left pending.
func TestProviderDelivery_UnreadablePayloadFailsDeliveriesOnTheLastAttempt(t *testing.T) {
	env := openTestEnv(t)
	env.useInstalledProviders(t, map[string]string{pushConnector: providerselect.CategoryPush})
	worker := env.connectorWorker(t, pushConnector, providerselect.CategoryPush)
	userID := env.createUser(t, "Esi", "")
	env.registerDevice(t, userID, "android", "tok-a")
	env.sendParked(t, userID, nil, Options{})

	job := env.providerJob(t, JobTypePushSend, 5, 5)
	job.Args.Payload = []byte{0xc1}
	if err := worker.Work(t.Context(), job); err == nil {
		t.Fatal("Work() = nil for an undecodable payload")
	}
	if got := env.channelDeliveries(t, push); len(got) != 1 || got[0].status != notifications.DeliveryFailed || got[0].attempts != 5 {
		t.Errorf("push deliveries = %+v, want one failed at attempt 5", got)
	}
}
