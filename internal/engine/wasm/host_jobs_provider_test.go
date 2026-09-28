package wasm

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/vmihailenco/msgpack/v5"
)

var hostJobsProviderCallerModule = buildHostCallerModule("host.jobs",
	[]string{"enqueue_provider", "enqueue_provider_tx", "dispatch_provider_sync", "set_result"})

// fakeSyncJobDispatcher stands in for jobdispatch.SyncDispatcher: it
// records the request and answers with a scripted status/result/error, or
// blocks until ctx is done when block is set.
type fakeSyncJobDispatcher struct {
	status int32
	result []byte
	err    error
	block  bool
	delay  time.Duration // sleeps this long, ignoring ctx, before answering

	got SyncJobRequest
}

func (d *fakeSyncJobDispatcher) DispatchJobSync(ctx context.Context, req SyncJobRequest) (int32, []byte, error) {
	d.got = req
	if d.block {
		<-ctx.Done()
		return 0, nil, ctx.Err()
	}
	time.Sleep(d.delay)
	return d.status, d.result, d.err
}

type providerJobsFixture struct {
	conn       *sql.DB
	runtime    *Runtime
	inst       *ModuleInstance
	modCtx     *ModuleContext
	tenantID   string
	tenantSlug string
}

// newProviderJobsFixture wires a Runtime to the real providerselect.Store
// against compose.dev.yml Postgres, with a fresh tenant and a caller module
// ("notifications") holding jobs.enqueue.
func newProviderJobsFixture(t *testing.T, caps abi.CapabilitySet) *providerJobsFixture {
	t.Helper()

	f := newProviderTenant(t)
	r := newHostDBTestRuntime(t, f.conn, 10)
	r.SetProviderStore(providerselect.NewStore(f.conn))

	mc := NewModuleContext("req-1", "notifications", "user-1", "", nil, nil, f.tenantID, f.tenantSlug, "trace-1", caps, nil, ModuleSnapshot{})
	inst := newHostCallerInstance(t, t.Context(), r, hostJobsProviderCallerModule, mc)

	f.runtime, f.inst, f.modCtx = r, inst, mc
	return f
}

// newProviderTenant creates a fresh tenant against compose.dev.yml
// Postgres, with every table provider resolution reads bootstrapped.
func newProviderTenant(t *testing.T) *providerJobsFixture {
	t.Helper()
	ctx := t.Context()

	conn := openTestPrimaryDB(t)
	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap: %v", err)
	}
	if err := billing.NewStore(conn).Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap: %v", err)
	}
	if err := user.NewStore(conn).Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap: %v", err)
	}
	providers := providerselect.NewStore(conn)
	if err := providers.Bootstrap(ctx); err != nil {
		t.Fatalf("providerselect Bootstrap: %v", err)
	}

	slug := fmt.Sprintf("providerjobs%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Provider Jobs Test Co")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	return &providerJobsFixture{conn: conn, tenantID: tt.ID, tenantSlug: slug}
}

func newHostCallerInstance(t *testing.T, ctx context.Context, r *Runtime, wasmBytes []byte, mc *ModuleContext) *ModuleInstance {
	t.Helper()

	compiled, err := r.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("provider-caller-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })
	return inst
}

// install writes the tenant_module_settings row module install would.
func (f *providerJobsFixture) install(t *testing.T, moduleName, category string, enabled bool) {
	t.Helper()
	if _, err := f.conn.Exec(`
		INSERT INTO system.tenant_module_settings (tenant_id, module_name, enabled, provider_category)
		VALUES ($1, $2, $3, $4)
	`, f.tenantID, moduleName, enabled, category); err != nil {
		t.Fatalf("install %s: %v", moduleName, err)
	}
}

func (f *providerJobsFixture) enqueue(t *testing.T, in abiv1.JobsEnqueueProviderInput) abiv1.Envelope {
	t.Helper()
	return callHost(t, t.Context(), f.inst, "call_enqueue_provider", in)
}

func (f *providerJobsFixture) dispatch(t *testing.T, in abiv1.JobsDispatchProviderSyncInput) abiv1.Envelope {
	t.Helper()
	return callHost(t, t.Context(), f.inst, "call_dispatch_provider_sync", in)
}

func decodeProviderEnqueueOutput(t *testing.T, env abiv1.Envelope) abiv1.JobsEnqueueProviderOutput {
	t.Helper()
	if !env.OK {
		t.Fatalf("enqueue_provider failed: %+v", env.Error)
	}
	var out abiv1.JobsEnqueueProviderOutput
	if err := msgpack.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	return out
}

func requireHostErrorCode(t *testing.T, env abiv1.Envelope, code string) {
	t.Helper()
	if env.OK || env.Error == nil || env.Error.Code != code {
		t.Fatalf("env = %+v (error %+v), want %s", env, env.Error, code)
	}
}

func loadJobRow(t *testing.T, conn *sql.DB, jobID string) (jobqueue.WASMJobArgs, jobMetadata) {
	t.Helper()
	id, err := jobqueue.DecodeJobID(jobID)
	if err != nil {
		t.Fatalf("DecodeJobID(%q): %v", jobID, err)
	}
	var argsRaw, metadataRaw []byte
	if err := conn.QueryRow(`SELECT args, metadata FROM system.river_job WHERE id = $1`, id).Scan(&argsRaw, &metadataRaw); err != nil {
		t.Fatalf("read job row: %v", err)
	}
	var args jobqueue.WASMJobArgs
	if err := json.Unmarshal(argsRaw, &args); err != nil {
		t.Fatalf("decode args: %v", err)
	}
	var metadata jobMetadata
	if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	return args, metadata
}

func TestHostJobs_EnqueueProvider_ResolvesSoleProvider(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_twilio", providerselect.CategorySMS, true)

	out := decodeProviderEnqueueOutput(t, f.enqueue(t, abiv1.JobsEnqueueProviderInput{
		Category: providerselect.CategorySMS, JobType: "sms_send",
		Payload: mustMarshalPayload(t, map[string]any{"schema_version": 1, "to": "+233200000000"}),
	}))
	if out.ResolvedModule != "connector_twilio" {
		t.Errorf("ResolvedModule = %q, want connector_twilio", out.ResolvedModule)
	}

	args, metadata := loadJobRow(t, f.conn, out.JobID)
	if args.ModuleName != "connector_twilio" || args.JobType != "sms_send" || args.ProviderCategory != providerselect.CategorySMS ||
		args.TenantID != f.tenantID || args.EnqueuedBy != "notifications" {
		t.Errorf("args = %+v", args)
	}
	if args.Queue != jobqueue.QueueDefault || args.MaxAttempts != defaultJobMaxAttempts {
		t.Errorf("queue/max_attempts = %s/%d, want engine defaults", args.Queue, args.MaxAttempts)
	}
	want := jobMetadata{TenantID: f.tenantID, ModuleName: "connector_twilio", TraceID: "trace-1", EnqueuedBy: "notifications"}
	if metadata != want {
		t.Errorf("metadata = %+v, want %+v", metadata, want)
	}
}

func TestHostJobs_EnqueueProvider_UsesTenantSelection(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_twilio", providerselect.CategorySMS, true)
	f.install(t, "connector_africastalking", providerselect.CategorySMS, true)

	requireHostErrorCode(t, f.enqueue(t, abiv1.JobsEnqueueProviderInput{Category: providerselect.CategorySMS, JobType: "sms_send"}),
		abiv1.ErrCodeJobsNoProviderSelected)

	if _, err := providerselect.NewStore(f.conn).SetPrimary(t.Context(), f.tenantID, "connector_africastalking", ""); err != nil {
		t.Fatalf("SetPrimary: %v", err)
	}
	out := decodeProviderEnqueueOutput(t, f.enqueue(t, abiv1.JobsEnqueueProviderInput{Category: providerselect.CategorySMS, JobType: "sms_send"}))
	if out.ResolvedModule != "connector_africastalking" {
		t.Errorf("ResolvedModule = %q, want the selected connector_africastalking", out.ResolvedModule)
	}
}

func TestHostJobs_EnqueueProvider_NoProviderInstalled(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_twilio", providerselect.CategorySMS, false)

	requireHostErrorCode(t, f.enqueue(t, abiv1.JobsEnqueueProviderInput{Category: providerselect.CategorySMS, JobType: "sms_send"}),
		abiv1.ErrCodeJobsNoProviderInstalled)
	if got := countWASMJobs(t, f.conn, f.tenantID); got != 0 {
		t.Fatalf("job count = %d, want 0", got)
	}
}

func TestHostJobs_EnqueueProvider_ExplicitProviderModule(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_paystack", providerselect.CategoryPayment, true)
	f.install(t, "connector_mtn_momo", providerselect.CategoryPayment, true)
	f.install(t, "connector_stripe", providerselect.CategoryPayment, false)
	f.install(t, "connector_twilio", providerselect.CategorySMS, true)

	out := decodeProviderEnqueueOutput(t, f.enqueue(t, abiv1.JobsEnqueueProviderInput{
		Category: providerselect.CategoryPayment, JobType: "payment_charge", ProviderModule: "connector_mtn_momo",
	}))
	if out.ResolvedModule != "connector_mtn_momo" {
		t.Errorf("ResolvedModule = %q, want connector_mtn_momo", out.ResolvedModule)
	}

	for _, module := range []string{"connector_stripe", "connector_twilio", "connector_missing"} {
		requireHostErrorCode(t, f.enqueue(t, abiv1.JobsEnqueueProviderInput{
			Category: providerselect.CategoryPayment, JobType: "payment_charge", ProviderModule: module,
		}), abiv1.ErrCodeJobsProviderModuleNotEnabled)
	}
}

func TestHostJobs_EnqueueProvider_InvalidCategory(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_paystack", providerselect.CategoryPayment, true)

	// A multi-active category is never resolved, even with one provider.
	requireHostErrorCode(t, f.enqueue(t, abiv1.JobsEnqueueProviderInput{Category: providerselect.CategoryPayment, JobType: "payment_charge"}),
		abiv1.ErrCodeJobsInvalidProviderCategory)
	requireHostErrorCode(t, f.enqueue(t, abiv1.JobsEnqueueProviderInput{Category: "email_provider", JobType: "email_send"}),
		abiv1.ErrCodeJobsInvalidProviderCategory)
	requireHostErrorCode(t, f.enqueue(t, abiv1.JobsEnqueueProviderInput{Category: providerselect.CategoryPayment, ProviderModule: "connector_paystack"}),
		abiv1.ErrCodeJobsInvalidOptions)
}

// Idempotency keys are scoped to the enqueuing module too: two modules
// sending through the same provider with the same key both get a job.
func TestHostJobs_EnqueueProvider_IdempotencyKeyScopedToEnqueuingModule(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_twilio", providerselect.CategorySMS, true)
	in := abiv1.JobsEnqueueProviderInput{
		Category: providerselect.CategorySMS, JobType: "sms_send",
		Opts: abiv1.JobEnqueueOptions{IdempotencyKey: "reminder-42"},
	}

	first := decodeProviderEnqueueOutput(t, f.enqueue(t, in))
	repeat := decodeProviderEnqueueOutput(t, f.enqueue(t, in))
	if !repeat.Deduplicated || repeat.JobID != first.JobID {
		t.Errorf("repeat from the same module = %+v, want deduplicated onto %s", repeat, first.JobID)
	}

	crmCtx := NewModuleContext("req-2", "crm", "user-1", "", nil, nil, f.tenantID, f.tenantSlug, "trace-2", abi.CapJobsEnqueue, nil, ModuleSnapshot{})
	crm := newHostCallerInstance(t, t.Context(), f.runtime, hostJobsProviderCallerModule, crmCtx)
	other := decodeProviderEnqueueOutput(t, callHost(t, t.Context(), crm, "call_enqueue_provider", in))
	if other.Deduplicated || other.JobID == first.JobID {
		t.Errorf("same key from another module = %+v, want its own job", other)
	}
	if got := countWASMJobs(t, f.conn, f.tenantID); got != 2 {
		t.Fatalf("job count = %d, want 2", got)
	}
}

func TestHostJobs_EnqueueProviderTx_InsertsJobOnlyOnCommit(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_twilio", providerselect.CategorySMS, true)
	ctx := t.Context()

	enqueueTx := func(txID string) abiv1.Envelope {
		return callHost(t, ctx, f.inst, "call_enqueue_provider_tx", abiv1.JobsEnqueueProviderTxInput{
			TxID: txID, Category: providerselect.CategorySMS, JobType: "sms_send",
		})
	}

	rollbackTxID, rollbackTx := beginAndRegisterTx(t, ctx, f.conn, f.modCtx)
	decodeProviderEnqueueOutput(t, enqueueTx(rollbackTxID))
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	f.modCtx.RemoveTransaction(rollbackTxID)
	if got := countWASMJobs(t, f.conn, f.tenantID); got != 0 {
		t.Fatalf("job count after rollback = %d, want 0", got)
	}

	commitTxID, commitTx := beginAndRegisterTx(t, ctx, f.conn, f.modCtx)
	out := decodeProviderEnqueueOutput(t, enqueueTx(commitTxID))
	if err := commitTx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	f.modCtx.RemoveTransaction(commitTxID)
	if got := countWASMJobs(t, f.conn, f.tenantID); got != 1 {
		t.Fatalf("job count after commit = %d, want 1", got)
	}
	if out.ResolvedModule != "connector_twilio" {
		t.Errorf("ResolvedModule = %q, want connector_twilio", out.ResolvedModule)
	}

	requireHostErrorCode(t, enqueueTx("does-not-exist"), abiv1.ErrCodeTransactionNotFound)
}

func TestHostJobs_ProviderFunctions_CapabilityDenied(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapabilitySet(0))

	for _, fn := range []string{"call_enqueue_provider", "call_enqueue_provider_tx", "call_dispatch_provider_sync"} {
		requireHostErrorCode(t, callHost(t, t.Context(), f.inst, fn, abiv1.JobsEnqueueProviderInput{Category: providerselect.CategorySMS}),
			abiv1.ErrCodeCapabilityDenied)
	}
}

func TestHostJobs_DispatchProviderSync_ReturnsHandlerResult(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_paystack", providerselect.CategoryPayment, true)
	result := mustMarshalPayload(t, map[string]any{"checkout_url": "https://checkout.example/abc"})
	d := &fakeSyncJobDispatcher{result: result}
	f.runtime.SetSyncJobDispatcher(d)

	payload := mustMarshalPayload(t, map[string]any{"schema_version": 1, "amount": 1000})
	env := f.dispatch(t, abiv1.JobsDispatchProviderSyncInput{
		Category: providerselect.CategoryPayment, ProviderModule: "connector_paystack", JobType: "payment_charge", Payload: payload,
	})
	if !env.OK {
		t.Fatalf("dispatch failed: %+v", env.Error)
	}
	var out abiv1.JobsDispatchProviderSyncOutput
	if err := msgpack.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if string(out.ResultPayload) != string(result) {
		t.Errorf("ResultPayload = %x, want %x", out.ResultPayload, result)
	}

	want := SyncJobRequest{
		ModuleName: "connector_paystack", JobType: "payment_charge", Payload: payload,
		TenantID: f.tenantID, TenantSlug: f.modCtx.TenantSlug, TraceID: "trace-1",
	}
	if d.got.ModuleName != want.ModuleName || d.got.JobType != want.JobType || string(d.got.Payload) != string(want.Payload) ||
		d.got.TenantID != want.TenantID || d.got.TenantSlug != want.TenantSlug || d.got.TraceID != want.TraceID {
		t.Errorf("dispatched %+v, want %+v", d.got, want)
	}
	if got := countWASMJobs(t, f.conn, f.tenantID); got != 0 {
		t.Errorf("job count = %d, want 0: sync dispatch inserts no job", got)
	}
}

func TestHostJobs_DispatchProviderSync_NoResultIsNotAnError(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_paystack", providerselect.CategoryPayment, true)
	f.runtime.SetSyncJobDispatcher(&fakeSyncJobDispatcher{})

	env := f.dispatch(t, abiv1.JobsDispatchProviderSyncInput{
		Category: providerselect.CategoryPayment, ProviderModule: "connector_paystack", JobType: "payment_charge",
	})
	if !env.OK {
		t.Fatalf("dispatch failed: %+v", env.Error)
	}
	var out abiv1.JobsDispatchProviderSyncOutput
	if err := msgpack.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if out.ResultPayload != nil {
		t.Errorf("ResultPayload = %x, want nil", out.ResultPayload)
	}
}

func TestHostJobs_DispatchProviderSync_HandlerFailures(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_paystack", providerselect.CategoryPayment, true)
	in := abiv1.JobsDispatchProviderSyncInput{
		Category: providerselect.CategoryPayment, ProviderModule: "connector_paystack", JobType: "payment_charge",
	}

	for _, tt := range []struct {
		name      string
		d         *fakeSyncJobDispatcher
		code      string
		wantRetry bool
	}{
		{"retryable status", &fakeSyncJobDispatcher{status: 1}, abiv1.ErrCodeJobsHandlerFailed, true},
		{"permanent status", &fakeSyncJobDispatcher{status: 2}, abiv1.ErrCodeJobsHandlerFailed, false},
		{"trap", &fakeSyncJobDispatcher{err: errors.New("wasm trap: unreachable")}, abiv1.ErrCodeJobsHandlerFailed, false},
		{"target unavailable", &fakeSyncJobDispatcher{err: fmt.Errorf("%w: module %q is not ready", ErrSyncJobTargetUnavailable, "connector_paystack")}, abiv1.ErrCodeUnavailable, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f.runtime.SetSyncJobDispatcher(tt.d)
			env := f.dispatch(t, in)
			requireHostErrorCode(t, env, tt.code)
			if env.Error.Retry != tt.wantRetry {
				t.Errorf("Retry = %v, want %v", env.Error.Retry, tt.wantRetry)
			}
		})
	}
}

func TestHostJobs_DispatchProviderSync_Timeout(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_paystack", providerselect.CategoryPayment, true)
	f.runtime.SetSyncJobDispatcher(&fakeSyncJobDispatcher{block: true})

	start := time.Now()
	env := f.dispatch(t, abiv1.JobsDispatchProviderSyncInput{
		Category: providerselect.CategoryPayment, ProviderModule: "connector_paystack", JobType: "payment_charge", TimeoutMs: 50,
	})
	requireHostErrorCode(t, env, abiv1.ErrCodeJobsSyncDispatchTimeout)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("dispatch took %s, want it bounded by timeout_ms", elapsed)
	}
}

// A handler that returns successfully as the deadline passes has still
// run: its result is returned, not a retryable timeout.
func TestHostJobs_DispatchProviderSync_SuccessAtDeadlineIsNotATimeout(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_paystack", providerselect.CategoryPayment, true)
	result := mustMarshalPayload(t, map[string]any{"checkout_url": "https://checkout.example/abc"})
	f.runtime.SetSyncJobDispatcher(&fakeSyncJobDispatcher{result: result, delay: 100 * time.Millisecond})

	env := f.dispatch(t, abiv1.JobsDispatchProviderSyncInput{
		Category: providerselect.CategoryPayment, ProviderModule: "connector_paystack", JobType: "payment_charge", TimeoutMs: 20,
	})
	if !env.OK {
		t.Fatalf("dispatch failed: %+v, want the handler's result", env.Error)
	}
	var out abiv1.JobsDispatchProviderSyncOutput
	if err := msgpack.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if string(out.ResultPayload) != string(result) {
		t.Errorf("ResultPayload = %x, want %x", out.ResultPayload, result)
	}
}

func TestHostJobs_DispatchProviderSync_RejectedInsideTransaction(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_paystack", providerselect.CategoryPayment, true)
	d := &fakeSyncJobDispatcher{}
	f.runtime.SetSyncJobDispatcher(d)

	txID, tx := beginAndRegisterTx(t, t.Context(), f.conn, f.modCtx)
	t.Cleanup(func() { _ = tx.Rollback(); f.modCtx.RemoveTransaction(txID) })

	requireHostErrorCode(t, f.dispatch(t, abiv1.JobsDispatchProviderSyncInput{
		Category: providerselect.CategoryPayment, ProviderModule: "connector_paystack", JobType: "payment_charge",
	}), abiv1.ErrCodeJobsSyncInTransaction)
	if d.got.ModuleName != "" {
		t.Error("handler was dispatched despite the open transaction")
	}
}

func TestHostJobs_DispatchProviderSync_ValidatesTarget(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapJobsEnqueue)
	f.install(t, "connector_paystack", providerselect.CategoryPayment, false)
	f.runtime.SetSyncJobDispatcher(&fakeSyncJobDispatcher{})

	for _, tt := range []struct {
		name string
		in   abiv1.JobsDispatchProviderSyncInput
		code string
	}{
		{"missing provider_module", abiv1.JobsDispatchProviderSyncInput{Category: providerselect.CategoryPayment, JobType: "payment_charge"}, abiv1.ErrCodeJobsProviderModuleNotEnabled},
		{"disabled module", abiv1.JobsDispatchProviderSyncInput{Category: providerselect.CategoryPayment, ProviderModule: "connector_paystack", JobType: "payment_charge"}, abiv1.ErrCodeJobsProviderModuleNotEnabled},
		{"unknown category", abiv1.JobsDispatchProviderSyncInput{Category: "email_provider", ProviderModule: "connector_paystack", JobType: "email_send"}, abiv1.ErrCodeJobsInvalidProviderCategory},
		{"negative timeout", abiv1.JobsDispatchProviderSyncInput{Category: providerselect.CategoryPayment, ProviderModule: "connector_paystack", JobType: "payment_charge", TimeoutMs: -1}, abiv1.ErrCodeJobsInvalidOptions},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requireHostErrorCode(t, f.dispatch(t, tt.in), tt.code)
		})
	}
}

func TestHostJobs_SetResult_RecordsOnlyWhenCapturing(t *testing.T) {
	f := newProviderJobsFixture(t, abi.CapabilitySet(0))
	value := mustMarshalPayload(t, map[string]any{"checkout_url": "https://checkout.example/abc"})

	// Not capturing: the async path, where set_result is a silent no-op.
	env := callHost(t, t.Context(), f.inst, "call_set_result", abiv1.JobsSetResultInput{Value: value})
	if !env.OK {
		t.Fatalf("set_result failed: %+v", env.Error)
	}
	if got := f.modCtx.JobResult(); got != nil {
		t.Fatalf("JobResult = %x without CaptureJobResult, want nil", got)
	}

	f.modCtx.CaptureJobResult()
	if env := callHost(t, t.Context(), f.inst, "call_set_result", abiv1.JobsSetResultInput{Value: value}); !env.OK {
		t.Fatalf("set_result failed: %+v", env.Error)
	}
	if got := f.modCtx.JobResult(); string(got) != string(value) {
		t.Fatalf("JobResult = %x, want %x", got, value)
	}
}

func TestValidateSyncProviderDispatch_TimeoutFallback(t *testing.T) {
	mc := NewModuleContext("req-1", "accounting", "", "", nil, nil, "tenant-1", "t", "", abi.CapJobsEnqueue, nil, ModuleSnapshot{})
	in := abiv1.JobsDispatchProviderSyncInput{ProviderModule: "connector_paystack", JobType: "payment_charge"}

	for _, tt := range []struct {
		configured time.Duration
		timeoutMs  int64
		want       time.Duration
	}{
		{0, 0, defaultSyncProviderTimeout},
		{20 * time.Second, 0, 20 * time.Second},
		{20 * time.Second, 500, 500 * time.Millisecond},
		{0, math.MaxInt64, time.Duration(math.MaxInt64/int64(time.Millisecond)) * time.Millisecond},
	} {
		r := &Runtime{syncProviderTimeout: tt.configured}
		in.TimeoutMs = tt.timeoutMs
		got, hostErr := r.validateSyncProviderDispatch(mc, in)
		if hostErr != nil || got != tt.want {
			t.Errorf("configured %s, timeout_ms %d: got %s, %v; want %s", tt.configured, tt.timeoutMs, got, hostErr, tt.want)
		}
	}
}

// TestHostcallFixture_EnqueueProviderTxFlow: a real compiled module's
// jobs.EnqueueProviderTx lands one sms_send job on the tenant's sole SMS
// provider once its transaction commits.
func TestHostcallFixture_EnqueueProviderTxFlow(t *testing.T) {
	f := newProviderTenant(t)
	f.install(t, "connector_twilio", providerselect.CategorySMS, true)
	wasmBytes := compileHostcallFixture(t)

	r := newHostcallTestRuntime(t, f.conn, 10)
	r.SetProviderStore(providerselect.NewStore(f.conn))
	mc := NewModuleContext("req-1", "notifications", "user-1", "", nil, nil, f.tenantID, f.tenantSlug, "trace-1", abi.CapDBWrite|abi.CapJobsEnqueue, nil, ModuleSnapshot{})

	out := callHostcallFixture(t, t.Context(), r, wasmBytes, mc, "run_enqueue_provider_tx_flow")
	if !out.OK {
		t.Fatalf("run_enqueue_provider_tx_flow failed: %s", out.Error)
	}
	args, _ := loadJobRow(t, f.conn, out.JobID)
	if args.ModuleName != "connector_twilio" || args.JobType != "sms_send" || args.ProviderCategory != providerselect.CategorySMS {
		t.Errorf("args = %+v, want sms_send on connector_twilio", args)
	}
}

// TestHostcallFixture_DispatchProviderSyncFlow: a real compiled module's
// jobs.DispatchProviderSync decodes the handler's result into its result
// pointer.
func TestHostcallFixture_DispatchProviderSyncFlow(t *testing.T) {
	f := newProviderTenant(t)
	f.install(t, "connector_paystack", providerselect.CategoryPayment, true)
	wasmBytes := compileHostcallFixture(t)

	r := newHostcallTestRuntime(t, f.conn, 10)
	r.SetProviderStore(providerselect.NewStore(f.conn))
	r.SetSyncJobDispatcher(&fakeSyncJobDispatcher{
		result: mustMarshalPayload(t, map[string]any{"checkout_url": "https://checkout.example/abc"}),
	})
	mc := NewModuleContext("req-1", "accounting", "user-1", "", nil, nil, f.tenantID, f.tenantSlug, "trace-1", abi.CapJobsEnqueue, nil, ModuleSnapshot{})

	out := callHostcallFixture(t, t.Context(), r, wasmBytes, mc, "run_dispatch_provider_sync_flow")
	if !out.OK {
		t.Fatalf("run_dispatch_provider_sync_flow failed: %s", out.Error)
	}
	if out.Result != "https://checkout.example/abc" {
		t.Errorf("result = %q, want the handler's checkout_url", out.Result)
	}
}
