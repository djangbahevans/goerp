package jobqueue_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
)

func TestReindexWorker_ReindexesAndDropsInvalidArtifacts(t *testing.T) {
	ctx := t.Context()
	pool, err := db.New(testDSN)
	if err != nil {
		t.Skipf("dev Postgres unreachable at %s (start compose.dev.yml): %v", testDSN, err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	pgx, err := db.NewPgxPool(ctx, testDSN)
	if err != nil {
		t.Fatalf("connect pgx pool: %v", err)
	}
	t.Cleanup(pgx.Close)
	if err := jobqueue.Migrate(ctx, pgx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// An invalid index named like an interrupted REINDEX CONCURRENTLY's.
	artifact := fmt.Sprintf("river_job_args_index_ccnew%d", time.Now().UnixNano()%1_000_000)
	if _, err := pool.Exec(`CREATE INDEX ` + artifact + ` ON ` + jobqueue.Schema + `.river_job (id)`); err != nil {
		t.Fatalf("create artifact index: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(`DROP INDEX IF EXISTS ` + jobqueue.Schema + `.` + artifact) })
	if _, err := pool.Exec(`UPDATE pg_index SET indisvalid = false WHERE indexrelid = $1::regclass`, jobqueue.Schema+"."+artifact); err != nil {
		t.Fatalf("mark artifact invalid: %v", err)
	}

	if err := (&jobqueue.ReindexWorker{Pool: pool}).Work(ctx, nil); err != nil {
		t.Fatalf("Work: %v", err)
	}

	var artifactExists, indexExists bool
	if err := pool.QueryRow(`SELECT to_regclass($1) IS NOT NULL, to_regclass($2) IS NOT NULL`,
		jobqueue.Schema+"."+artifact, jobqueue.Schema+".river_job_args_index").Scan(&artifactExists, &indexExists); err != nil {
		t.Fatalf("check indexes: %v", err)
	}
	if artifactExists {
		t.Errorf("invalid artifact %s still exists", artifact)
	}
	if !indexExists {
		t.Error("river_job_args_index missing after reindex")
	}
}
