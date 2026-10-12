package retention

import (
	"encoding/json/v2"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdbtest"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
)

func TestWorkerRecordsPreviewAndDeletionResults(t *testing.T) {
	f := newFixture(t,
		manifest.RetentionPolicy{Table: "retention_item", RetainDays: 30, Action: "delete"},
		manifest.RetentionPolicy{Table: "retention_item", RetainDays: 30, Action: "nullify"},
	)
	f.seed(t, f.tenant.Slug, "retention_item")
	var database string
	if err := f.pool.QueryRowContext(t.Context(), "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(t.Context(), "postgres://goerp:dev@localhost:15432/"+database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	driver := riverpgxv5.New(pool)
	schema := riverdbtest.TestSchema(t.Context(), t, driver, &riverdbtest.TestSchemaOpts{DisableReuse: true})
	worker := rivertest.NewWorker(t, driver, &river.Config{Schema: schema}, &Worker{Sweeper: f.sweeper})
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()

	for _, preview := range []bool{true, false} {
		worked, err := worker.Work(t.Context(), t, tx, jobqueue.RetentionSweepArgs{At: f.at, Preview: preview}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if worked.EventKind != river.EventKindJobCompleted {
			t.Fatalf("worker event = %s", worked.EventKind)
		}
		var metadata struct {
			Output Result `json:"output"`
		}
		if err := json.Unmarshal(worked.Job.Metadata, &metadata); err != nil {
			t.Fatal(err)
		}
		output := metadata.Output
		if !output.At.Equal(f.at) || output.Preview != preview || len(output.Policies) != 2 {
			t.Fatalf("output = %+v", output)
		}
		if p := output.Policies[0]; p.Affected != 1 || p.Status == "skipped" {
			t.Errorf("supported policy output = %+v", p)
		}
		if p := output.Policies[1]; p.Status != "skipped" || p.Diagnostic == "" {
			t.Errorf("unsupported policy output = %+v", p)
		}
		wantRows := 2
		if preview {
			wantRows = 3
		}
		if got := f.ids(t, f.tenant.Slug, "retention_item"); len(got) != wantRows {
			t.Errorf("rows = %v, want %d rows", got, wantRows)
		}
	}
}
