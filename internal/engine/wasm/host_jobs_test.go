package wasm

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/vmihailenco/msgpack/v5"
)

var hostJobsCallerModule = buildHostCallerModule("host.jobs", []string{"enqueue", "enqueue_tx"})

var testJobTypes = []manifest.JobType{
	{Name: "contacts_import", Queue: jobqueue.QueueBulk, MaxAttempts: 7, Priority: 80},
	{Name: "contacts_sync", Queue: jobqueue.QueueDefault, UniqueBy: "contact_id"},
}

func newJobsTestModuleContext(tenantID string, caps abi.CapabilitySet) *ModuleContext {
	return NewModuleContext("req-1", "contacts", "user-1", "", nil, nil, tenantID, "jobstest", "trace-1", caps, nil, ModuleSnapshot{JobTypes: testJobTypes})
}

func newHostJobsCaller(t *testing.T, ctx context.Context, r *Runtime, mc *ModuleContext) *ModuleInstance {
	t.Helper()

	compiled, err := r.wazero.CompileModule(ctx, hostJobsCallerModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("jobs-caller-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	return inst
}

func decodeEnqueueOutput(t *testing.T, env abiv1.Envelope) abiv1.JobsEnqueueOutput {
	t.Helper()
	if !env.OK {
		t.Fatalf("enqueue failed: %+v", env.Error)
	}
	var out abiv1.JobsEnqueueOutput
	if err := msgpack.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	return out
}

func countWASMJobs(t *testing.T, conn *sql.DB, tenantID string) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(
		`SELECT count(*) FROM system.river_job WHERE kind = 'wasm_job' AND args->>'tenant_id' = $1`,
		tenantID,
	).Scan(&count); err != nil {
		t.Fatalf("count river_job rows: %v", err)
	}
	return count
}

func TestBuildJobInsert_UndeclaredType(t *testing.T) {
	mc := newJobsTestModuleContext("tenant-1", abi.CapJobsEnqueue)

	_, _, hostErr := buildJobInsert(mc, "billing_run", nil, abiv1.JobEnqueueOptions{}, time.Now())
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeJobsUndeclaredType {
		t.Fatalf("hostErr = %v, want %s", hostErr, abiv1.ErrCodeJobsUndeclaredType)
	}
}

func TestBuildJobInsert_PayloadTooLarge(t *testing.T) {
	mc := newJobsTestModuleContext("tenant-1", abi.CapJobsEnqueue)

	if _, _, hostErr := buildJobInsert(mc, "contacts_import", make([]byte, maxJobPayloadBytes), abiv1.JobEnqueueOptions{}, time.Now()); hostErr != nil {
		t.Fatalf("payload at the limit rejected: %v", hostErr)
	}
	_, _, hostErr := buildJobInsert(mc, "contacts_import", make([]byte, maxJobPayloadBytes+1), abiv1.JobEnqueueOptions{}, time.Now())
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeJobsPayloadTooLarge {
		t.Fatalf("hostErr = %v, want %s", hostErr, abiv1.ErrCodeJobsPayloadTooLarge)
	}
}

func TestBuildJobInsert_FallsBackToManifestThenDefaults(t *testing.T) {
	mc := newJobsTestModuleContext("tenant-1", abi.CapJobsEnqueue)
	now := time.Unix(1_800_000_000, 0)

	args, opts, hostErr := buildJobInsert(mc, "contacts_import", nil, abiv1.JobEnqueueOptions{}, now)
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if opts.Queue != jobqueue.QueueBulk || args.Queue != jobqueue.QueueBulk {
		t.Errorf("queue = %q/%q, want manifest's %q", opts.Queue, args.Queue, jobqueue.QueueBulk)
	}
	if opts.MaxAttempts != 7 || args.MaxAttempts != 7 {
		t.Errorf("max attempts = %d/%d, want manifest's 7", opts.MaxAttempts, args.MaxAttempts)
	}
	if opts.Priority != 1 {
		t.Errorf("river priority = %d, want 1 for manifest priority 80", opts.Priority)
	}
	if !opts.ScheduledAt.Equal(now) {
		t.Errorf("ScheduledAt = %v, want %v", opts.ScheduledAt, now)
	}
	if opts.UniqueOpts.ByArgs {
		t.Error("UniqueOpts.ByArgs set without an idempotency key")
	}

	_, opts, hostErr = buildJobInsert(mc, "contacts_sync", nil, abiv1.JobEnqueueOptions{}, now)
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if opts.MaxAttempts != defaultJobMaxAttempts {
		t.Errorf("max attempts = %d, want default %d", opts.MaxAttempts, defaultJobMaxAttempts)
	}
	if opts.Priority != riverPriority(defaultJobPriority) {
		t.Errorf("river priority = %d, want %d", opts.Priority, riverPriority(defaultJobPriority))
	}
}

func TestBuildJobInsert_CallerOptionsOverrideManifest(t *testing.T) {
	mc := newJobsTestModuleContext("tenant-1", abi.CapJobsEnqueue)
	now := time.Unix(1_800_000_000, 0)

	args, opts, hostErr := buildJobInsert(mc, "contacts_import", nil, abiv1.JobEnqueueOptions{
		Queue: jobqueue.QueueCritical, Priority: 10, MaxAttempts: 2, DelayMs: 1500,
	}, now)
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if opts.Queue != jobqueue.QueueCritical || args.Queue != jobqueue.QueueCritical {
		t.Errorf("queue = %q/%q, want %q", opts.Queue, args.Queue, jobqueue.QueueCritical)
	}
	if opts.MaxAttempts != 2 {
		t.Errorf("max attempts = %d, want 2", opts.MaxAttempts)
	}
	if opts.Priority != 4 {
		t.Errorf("river priority = %d, want 4 for priority 10", opts.Priority)
	}
	if want := now.Add(1500 * time.Millisecond); !opts.ScheduledAt.Equal(want) {
		t.Errorf("ScheduledAt = %v, want %v", opts.ScheduledAt, want)
	}

	_, opts, hostErr = buildJobInsert(mc, "contacts_import", nil, abiv1.JobEnqueueOptions{ScheduledAt: 1_900_000_000}, now)
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if want := time.Unix(1_900_000_000, 0); !opts.ScheduledAt.Equal(want) {
		t.Errorf("ScheduledAt = %v, want %v", opts.ScheduledAt, want)
	}
}

func TestBuildJobInsert_InvalidOptions(t *testing.T) {
	mc := newJobsTestModuleContext("tenant-1", abi.CapJobsEnqueue)
	tests := []struct {
		name string
		opts abiv1.JobEnqueueOptions
	}{
		{"platform-reserved queue", abiv1.JobEnqueueOptions{Queue: jobqueue.QueueEvents}},
		{"unknown queue", abiv1.JobEnqueueOptions{Queue: "fast"}},
		{"priority above 100", abiv1.JobEnqueueOptions{Priority: 101}},
		{"negative priority", abiv1.JobEnqueueOptions{Priority: -1}},
		{"negative max_attempts", abiv1.JobEnqueueOptions{MaxAttempts: -1}},
		{"negative delay", abiv1.JobEnqueueOptions{DelayMs: -1}},
		{"delay with scheduled_at", abiv1.JobEnqueueOptions{DelayMs: 1, ScheduledAt: 1_900_000_000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, hostErr := buildJobInsert(mc, "contacts_import", nil, tt.opts, time.Now())
			if hostErr == nil || hostErr.Code != abiv1.ErrCodeJobsInvalidOptions {
				t.Fatalf("hostErr = %v, want %s", hostErr, abiv1.ErrCodeJobsInvalidOptions)
			}
		})
	}
}

func TestBuildJobInsert_UniqueByDerivesIdempotencyKey(t *testing.T) {
	mc := newJobsTestModuleContext("tenant-1", abi.CapJobsEnqueue)

	args, opts, hostErr := buildJobInsert(mc, "contacts_sync", mustMarshalPayload(t, map[string]any{"contact_id": "c-9"}), abiv1.JobEnqueueOptions{}, time.Now())
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if args.IdempotencyKey != "c-9" || !opts.UniqueOpts.ByArgs {
		t.Errorf("IdempotencyKey = %q, ByArgs = %v; want %q, true", args.IdempotencyKey, opts.UniqueOpts.ByArgs, "c-9")
	}

	args, _, _ = buildJobInsert(mc, "contacts_sync", mustMarshalPayload(t, map[string]any{"contact_id": "c-9"}), abiv1.JobEnqueueOptions{IdempotencyKey: "explicit"}, time.Now())
	if args.IdempotencyKey != "explicit" {
		t.Errorf("IdempotencyKey = %q, want the explicit key to win", args.IdempotencyKey)
	}
}

func TestRiverPriority(t *testing.T) {
	for p, want := range map[int]int{1: 4, 25: 4, 26: 3, 50: 3, 51: 2, 75: 2, 76: 1, 100: 1} {
		if got := riverPriority(p); got != want {
			t.Errorf("riverPriority(%d) = %d, want %d", p, got, want)
		}
	}
}

func TestHostJobs_EnqueueTx_InsertsJobOnlyOnCommit(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	tenantID := uuid.New().String()
	mc := newJobsTestModuleContext(tenantID, abi.CapJobsEnqueue)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostJobsCaller(t, ctx, r, mc)

	rollbackTxID, rollbackTx := beginAndRegisterTx(t, ctx, primaryDB, mc)
	decodeEnqueueOutput(t, callHost(t, ctx, inst, "call_enqueue_tx", abiv1.JobsEnqueueTxInput{
		TxID: rollbackTxID, Type: "contacts_import", Payload: mustMarshalPayload(t, map[string]any{"file_id": "1"}),
	}))
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := countWASMJobs(t, primaryDB, tenantID); got != 0 {
		t.Fatalf("job count after rollback = %d, want 0", got)
	}

	commitTxID, commitTx := beginAndRegisterTx(t, ctx, primaryDB, mc)
	out := decodeEnqueueOutput(t, callHost(t, ctx, inst, "call_enqueue_tx", abiv1.JobsEnqueueTxInput{
		TxID: commitTxID, Type: "contacts_import", Payload: mustMarshalPayload(t, map[string]any{"file_id": "2"}),
	}))
	if err := commitTx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := countWASMJobs(t, primaryDB, tenantID); got != 1 {
		t.Fatalf("job count after commit = %d, want 1", got)
	}
	if _, err := jobqueue.DecodeJobID(out.JobID); err != nil {
		t.Errorf("JobID %q: %v", out.JobID, err)
	}
}

func TestHostJobs_EnqueueTx_UnknownTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	mc := newJobsTestModuleContext(uuid.New().String(), abi.CapJobsEnqueue)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostJobsCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_enqueue_tx", abiv1.JobsEnqueueTxInput{TxID: "does-not-exist", Type: "contacts_import"})
	if env.OK || env.Error.Code != abiv1.ErrCodeTransactionNotFound {
		t.Fatalf("env = %+v, want %s", env, abiv1.ErrCodeTransactionNotFound)
	}
}

func TestHostJobs_Enqueue_IdempotencyKeyDeduplicates(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	tenantID := uuid.New().String()
	mc := newJobsTestModuleContext(tenantID, abi.CapJobsEnqueue)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostJobsCaller(t, ctx, r, mc)

	enqueue := func(key string, payload map[string]any) abiv1.JobsEnqueueOutput {
		return decodeEnqueueOutput(t, callHost(t, ctx, inst, "call_enqueue", abiv1.JobsEnqueueInput{
			Type: "contacts_import", Payload: mustMarshalPayload(t, payload),
			Opts: abiv1.JobEnqueueOptions{IdempotencyKey: key},
		}))
	}

	first := enqueue("import:file-1", map[string]any{"file_id": "file-1"})
	if first.Deduplicated {
		t.Fatal("first enqueue reported deduplicated")
	}
	repeat := enqueue("import:file-1", map[string]any{"file_id": "file-1", "retry": true})
	if !repeat.Deduplicated || repeat.JobID != first.JobID {
		t.Errorf("repeat = %+v, want deduplicated onto %s", repeat, first.JobID)
	}
	fresh := enqueue("import:file-2", map[string]any{"file_id": "file-2"})
	if fresh.Deduplicated || fresh.JobID == first.JobID {
		t.Errorf("fresh key = %+v, want a new job", fresh)
	}

	if got := countWASMJobs(t, primaryDB, tenantID); got != 2 {
		t.Fatalf("job count = %d, want 2", got)
	}
}

func TestHostJobs_Enqueue_StampsMetadataAndArgs(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	tenantID := uuid.New().String()
	mc := newJobsTestModuleContext(tenantID, abi.CapJobsEnqueue)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostJobsCaller(t, ctx, r, mc)

	out := decodeEnqueueOutput(t, callHost(t, ctx, inst, "call_enqueue", abiv1.JobsEnqueueInput{Type: "contacts_import"}))
	id, err := jobqueue.DecodeJobID(out.JobID)
	if err != nil {
		t.Fatalf("DecodeJobID: %v", err)
	}

	var queue string
	var maxAttempts int
	var metadataRaw, argsRaw []byte
	if err := primaryDB.QueryRowContext(ctx,
		`SELECT queue, max_attempts, metadata, args FROM system.river_job WHERE id = $1`, id,
	).Scan(&queue, &maxAttempts, &metadataRaw, &argsRaw); err != nil {
		t.Fatalf("read job row: %v", err)
	}
	if queue != jobqueue.QueueBulk || maxAttempts != 7 {
		t.Errorf("queue/max_attempts = %s/%d, want bulk/7", queue, maxAttempts)
	}

	var metadata jobMetadata
	if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if want := (jobMetadata{TenantID: tenantID, ModuleName: "contacts", TraceID: "trace-1"}); metadata != want {
		t.Errorf("metadata = %+v, want %+v", metadata, want)
	}

	var args jobqueue.WASMJobArgs
	if err := json.Unmarshal(argsRaw, &args); err != nil {
		t.Fatalf("decode args: %v", err)
	}
	if args.ModuleName != "contacts" || args.JobType != "contacts_import" || args.TenantID != tenantID || args.TraceID != "trace-1" {
		t.Errorf("args = %+v", args)
	}
}

func TestHostJobs_Enqueue_UndeclaredType(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	tenantID := uuid.New().String()
	mc := newJobsTestModuleContext(tenantID, abi.CapJobsEnqueue)
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostJobsCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_enqueue", abiv1.JobsEnqueueInput{Type: "billing_run"})
	if env.OK || env.Error.Code != abiv1.ErrCodeJobsUndeclaredType {
		t.Fatalf("env = %+v, want %s", env, abiv1.ErrCodeJobsUndeclaredType)
	}
	if got := countWASMJobs(t, primaryDB, tenantID); got != 0 {
		t.Fatalf("job count = %d, want 0", got)
	}
}

func TestHostJobs_Enqueue_CapabilityDenied(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := t.Context()

	mc := newJobsTestModuleContext(uuid.New().String(), abi.CapabilitySet(0))
	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostJobsCaller(t, ctx, r, mc)

	for _, fn := range []string{"call_enqueue", "call_enqueue_tx"} {
		env := callHost(t, ctx, inst, fn, abiv1.JobsEnqueueInput{Type: "contacts_import"})
		if env.OK || env.Error.Code != abiv1.ErrCodeCapabilityDenied {
			t.Errorf("%s: env = %+v, want %s", fn, env, abiv1.ErrCodeCapabilityDenied)
		}
	}
}
