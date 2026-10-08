package cronsched

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/cronsettings"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdbtest"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

const jobsTestDSN = "postgres://goerp:dev@localhost:6432/goerp"

// Two engines (or a retried tick) working the same minute must leave one
// handle_cron job per tenant: the idempotency key is unique across every job
// state, so even a completed run is not enqueued again.
func TestTickEnqueuesOneJobPerTenantAcrossRepeatedTicks(t *testing.T) {
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, jobsTestDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("dev Postgres unreachable at %s (start compose.dev.yml): %v", jobsTestDSN, err)
	}

	// An isolated schema, so no other engine polling the shared dev
	// database claims these jobs (see jobqueuetest.New).
	driver := riverpgxv5.New(pool)
	schema := riverdbtest.TestSchema(ctx, t, driver, &riverdbtest.TestSchemaOpts{DisableReuse: true})
	client, err := river.NewClient(driver, &river.Config{Schema: schema})
	if err != nil {
		t.Fatalf("river.NewClient: %v", err)
	}

	e := newEnv(t,
		map[string]*module.LoadedModule{"crm": readyModule("crm", cronJob("dedupe", "* * * * *"))},
		fakeEntitlements{"t1": {"crm"}, "t2": {"crm"}}, "t1", "t2",
	)
	e.worker.Inserter = client

	for range 3 {
		if err := e.tick(t, "2026-10-08 10:00:00"); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	states := e.worker.Settings.(fakeSettings)
	identity := cronsettings.Identity{Module: "crm", Name: "dedupe"}
	for _, slug := range []string{"slug-t1", "slug-t2"} {
		state := states[slug][identity]
		state.Generation = "a-new-generation-after-toggle"
		states[slug][identity] = state
	}
	if err := e.tick(t, "2026-10-08 10:00:00"); err != nil {
		t.Fatal(err)
	}
	if err := e.tick(t, "2026-10-08 10:01:00"); err != nil {
		t.Fatalf("tick: %v", err)
	}

	list, err := client.JobList(ctx, river.NewJobListParams().Kinds(jobqueue.WASMJobArgs{}.Kind()).States(rivertype.JobStateAvailable).First(100))
	if err != nil {
		t.Fatalf("JobList: %v", err)
	}
	if got, want := len(list.Jobs), 4; got != want {
		t.Errorf("%d cron jobs enqueued, want %d (2 tenants x 2 distinct minutes)", got, want)
	}
	for _, row := range list.Jobs {
		if row.Queue != jobqueue.QueueBulk || row.MaxAttempts != maxAttempts {
			t.Errorf("job %d queue = %q, max attempts = %d; want the cron job's queue %q and %d attempts", row.ID, row.Queue, row.MaxAttempts, jobqueue.QueueBulk, maxAttempts)
		}
	}
}
