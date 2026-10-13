package jobdispatch

import (
	"context"
	"encoding/json/v2"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/jobprogress"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func TestWorkerPersistsProgressBeforeRiverCompletion(t *testing.T) {
	f := newJobIdentityFixture(t)
	f.setPolicy(t, "TRUE")
	store := jobprogress.New(f.admin)
	if err := store.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.worker.StartProgress = store.Begin
	args := f.args(f.userID)
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := f.admin.QueryRow(`INSERT INTO system.river_job(kind,args,state,queue,max_attempts,attempted_at) VALUES ('wasm_job',$1,'running',$2,3,NOW()) RETURNING id`, string(raw), "progress_"+uuid.New().String()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.admin.Exec(`DELETE FROM system.job_progress WHERE job_id=$1`, id) })
	job := &river.Job[jobqueue.WASMJobArgs]{JobRow: new(rivertype.JobRow{ID: id, Attempt: 1, MaxAttempts: 3}), Args: args}
	if err := f.worker.Work(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Read(t.Context(), id, f.tenant.ID, f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "running" || snapshot.Percent != 25 || snapshot.Message != "Reading identity" || snapshot.Result != nil {
		t.Fatalf("running snapshot=%+v", snapshot)
	}
	var result map[string]string
	var data []byte
	if err := f.admin.QueryRow(`SELECT result FROM system.job_progress WHERE job_id=$1`, id).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &result); err != nil || result["user_id"] != f.userID {
		t.Fatalf("persisted result=%v err=%v", result, err)
	}
}

func TestWorkerProgressIneligibleInvocations(t *testing.T) {
	f := newJobIdentityFixture(t)
	f.setPolicy(t, "TRUE")
	started := 0
	f.worker.StartProgress = func(int64) jobprogress.Recorder {
		started++
		return jobprogress.NewAttempt(func(_ context.Context, _ jobprogress.Report) error { return nil })
	}
	args := f.args("")
	if err := runWork(t, f.worker, args); err != nil {
		t.Fatal(err)
	}
	args = f.args(f.userID)
	args.IsDataMigration = true
	_ = runWork(t, f.worker, args)
	args = f.args(f.userID)
	args.IsCron = true
	_ = runWork(t, f.worker, args)
	args = f.args(f.userID)
	args.NotificationID = "delivery"
	_ = runWork(t, f.worker, args)
	status, _, err := f.sync.DispatchJobSync(t.Context(), wasm.SyncJobRequest{ModuleName: args.ModuleName, JobType: args.JobType, TenantID: args.TenantID, TenantSlug: f.tenant.Slug, UserID: f.userID})
	if err != nil || status != 0 {
		t.Fatalf("sync invocation=%d %v", status, err)
	}
	if started != 0 {
		t.Fatalf("started %d ineligible progress reporters", started)
	}
}
