package jobprogress_test

import (
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/jobprogress"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testStore(t *testing.T) (*jobprogress.Store, *sql.DB) {
	t.Helper()
	dsn := "postgres://goerp:dev@localhost:15432/goerp"
	conn, err := db.New(dsn)
	if err != nil {
		t.Skipf("Postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := jobqueue.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := jobprogress.New(conn)
	if err := store.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	return store, conn
}

func TestStoredSnapshotsOwnershipStatesAndRetry(t *testing.T) {
	store, conn := testStore(t)
	tenant, user := uuid.New().String(), uuid.New().String()
	raw, err := json.Marshal(map[string]any{"tenant_id": tenant, "user_id": user})
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = conn.QueryRowContext(t.Context(), `INSERT INTO system.river_job (kind,args,state,queue,max_attempts) VALUES ('wasm_job',$1,'scheduled',$2,3) RETURNING id`, string(raw), "progress_"+tenant).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.job_progress WHERE job_id=$1`, id)
		_, _ = conn.Exec(`DELETE FROM system.river_job WHERE id=$1`, id)
	})

	for _, identity := range [][2]string{{tenant, "other"}, {"other", user}, {tenant, ""}} {
		if _, err := store.Read(t.Context(), id, identity[0], identity[1]); !errors.Is(err, jobprogress.ErrNotFound) {
			t.Fatalf("identity %v err=%v", identity, err)
		}
	}
	for _, flag := range []string{"is_data_migration", "is_cron", "notification_id"} {
		args := map[string]any{"tenant_id": tenant, "user_id": user}
		if flag == "notification_id" {
			args[flag] = "delivery"
		} else {
			args[flag] = true
		}
		bytes, _ := json.Marshal(args)
		if _, err := conn.Exec(`UPDATE system.river_job SET args=$1 WHERE id=$2`, string(bytes), id); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Read(t.Context(), id, tenant, user); !errors.Is(err, jobprogress.ErrNotFound) {
			t.Fatalf("%s: %v", flag, err)
		}
	}
	if _, err := conn.Exec(`UPDATE system.river_job SET args=$1 WHERE id=$2`, string(raw), id); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(t.Context(), id, jobprogress.Report{Percent: 46, Message: "partial", HasProgress: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(t.Context(), id, jobprogress.Report{Result: []byte(`{"created":2}`)}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		river, status, code string
		percent             int
	}{
		{"available", "pending", "", 46}, {"scheduled", "pending", "", 46}, {"pending", "pending", "", 46},
		{"running", "running", "", 46}, {"retryable", "running", "", 46},
		{"completed", "complete", "", 100}, {"cancelled", "failed", "jobs.cancelled", 46}, {"discarded", "failed", "jobs.failed", 46},
	} {
		started := time.Now().UTC().Add(-time.Minute)
		first := started.Add(-time.Hour)
		errorJSON := fmt.Sprintf(`{"at":%q,"error":"secret","attempt":1}`, first.Format(time.RFC3339Nano))
		if _, err := conn.Exec(`UPDATE system.river_job SET state=$1,attempted_at=$2,finalized_at=CASE WHEN $5 THEN NOW() ELSE NULL END,errors=ARRAY[$3::jsonb] WHERE id=$4`, test.river, started, errorJSON, id, test.status == "complete" || test.status == "failed"); err != nil {
			t.Fatal(err)
		}
		snapshot, err := store.Read(t.Context(), id, tenant, user)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Status != test.status || snapshot.Percent != test.percent || snapshot.Message != "partial" {
			t.Fatalf("%s: %+v", test.river, snapshot)
		}
		if (snapshot.Result != nil) != (test.status == "complete") || (snapshot.Error != nil) != (test.status == "failed") {
			t.Fatalf("%s: result/error %+v", test.river, snapshot)
		}
		if snapshot.Error != nil && snapshot.Error.Code != test.code {
			t.Fatal(snapshot.Error)
		}
		if test.status == "pending" && snapshot.StartedAt != nil {
			t.Fatal("pending started_at")
		}
		if test.status != "pending" && (snapshot.StartedAt == nil || snapshot.StartedAt.Sub(first).Abs() > time.Microsecond) {
			t.Fatalf("first attempt timestamp=%v want=%v", snapshot.StartedAt, first)
		}
		if (snapshot.CompletedAt != nil) != (test.status == "complete" || test.status == "failed") {
			t.Fatal("terminal timestamp")
		}
	}

	if err := store.Save(t.Context(), id, jobprogress.Report{}); err != nil {
		t.Fatal(err)
	}
	var result []byte
	if err := conn.QueryRow(`SELECT result FROM system.job_progress WHERE job_id=$1`, id).Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("failed retry retained a result")
	}
	if _, err := conn.Exec(`DELETE FROM system.river_job WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(t.Context(), id, tenant, user); !errors.Is(err, jobprogress.ErrNotFound) {
		t.Fatal(err)
	}
	if err := store.Prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRow(`SELECT count(*) FROM system.job_progress WHERE job_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan count=%d err=%v", count, err)
	}
}
