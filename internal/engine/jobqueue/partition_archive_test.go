package jobqueue_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/storage/storagetest"
	"github.com/jackc/pgx/v5"
	"github.com/parquet-go/parquet-go"
)

type archiveBackend struct {
	storage.Backend
	uploads   int
	uploadErr error
	existsErr error
}

func (b *archiveBackend) Upload(ctx context.Context, key string, r io.Reader, opts storage.UploadOptions) (string, error) {
	b.uploads++
	if b.uploadErr != nil {
		return "", b.uploadErr
	}
	return b.Backend.Upload(ctx, key, r, opts)
}

func (b *archiveBackend) Exists(ctx context.Context, key string) (bool, error) {
	if b.existsErr != nil {
		return false, b.existsErr
	}
	return b.Backend.Exists(ctx, key)
}

type archiveFixture struct {
	admin   *sql.DB
	pool    *sql.DB
	schema  string
	month   time.Time
	backend *archiveBackend
	worker  *jobqueue.PartitionArchiveWorker
}

func newArchiveFixture(t *testing.T) *archiveFixture {
	t.Helper()
	admin := membershiptest.New(t)
	var database string
	if err := admin.QueryRowContext(t.Context(), "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	pool := membershiptest.Open(t, database, "schema_sync_user")
	slug := provisionLedgerTenant(t, pool)
	now := time.Now().UTC()
	month := time.Date(now.Year()-2, now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, table := range []string{"event_log", "audit_log"} {
		if _, err := pool.ExecContext(t.Context(), `SELECT partman.create_partition_time($1, $2::timestamptz[])`,
			"tenant_"+slug+"."+table, []time.Time{month, month.AddDate(1, 0, 0)}); err != nil {
			t.Fatalf("create archive partitions: %v", err)
		}
	}
	if err := enginetables.RevokeAppendOnlyMutations(t.Context(), pool, slug); err != nil {
		t.Fatal(err)
	}
	objectStorage, err := storagetest.New(t)
	if err != nil {
		t.Fatal(err)
	}
	backend := &archiveBackend{Backend: objectStorage}
	return &archiveFixture{
		admin:   admin,
		pool:    pool,
		schema:  "tenant_" + slug,
		month:   month,
		backend: backend,
		worker:  &jobqueue.PartitionArchiveWorker{Pool: pool, Backend: backend},
	}
}

func (f *archiveFixture) partition(table string) string {
	return table + "_p" + monthSuffix(f.month)
}

func (f *archiveFixture) key(table, partition string) string {
	return fmt.Sprintf("archive/%s/%s/%s.parquet.gz", strings.TrimPrefix(f.schema, "tenant_"), table, partition)
}

func (f *archiveFixture) insert(t *testing.T, table string, at time.Time) {
	t.Helper()
	parent := pgx.Identifier{f.schema, table}.Sanitize()
	var query string
	if table == "event_log" {
		query = "INSERT INTO " + parent + ` (event_name, event_version, emitter_module, payload, trace_id, trace_flags, emitted_at)
			VALUES ('system.tenant.created', 0, 'engine', decode('00ff1234', 'hex'), 'trace', 1, $1)`
	} else {
		query = "INSERT INTO " + parent + ` (table_name, record_id, operation, old_data, new_data, changed_at)
			VALUES ('contacts', '00000000-0000-0000-0000-000000000001', 'INSERT', NULL, '{"name":"Ama","tags":[1,2]}', $1)`
	}
	if _, err := f.pool.ExecContext(t.Context(), query, at); err != nil {
		t.Fatalf("insert %s: %v", table, err)
	}
}

func (f *archiveFixture) attached(t *testing.T, table, name string) bool {
	t.Helper()
	var attached bool
	if err := f.pool.QueryRowContext(t.Context(), `SELECT EXISTS (
		SELECT 1 FROM pg_inherits WHERE inhparent = $1::regclass AND inhrelid = to_regclass($2)
	)`, pgx.Identifier{f.schema, table}.Sanitize(), pgx.Identifier{f.schema, name}.Sanitize()).Scan(&attached); err != nil {
		t.Fatal(err)
	}
	return attached
}

func (f *archiveFixture) archiveRows(t *testing.T, key string) []map[string]any {
	t.Helper()
	object, _, err := f.backend.Download(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = object.Close() }()
	compressed, err := gzip.NewReader(object)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = compressed.Close() }()
	data, err := io.ReadAll(compressed)
	if err != nil {
		t.Fatal(err)
	}
	reader := parquet.NewGenericReader[any](bytes.NewReader(data))
	defer func() { _ = reader.Close() }()
	var records []map[string]any
	for {
		var record [1]any
		n, err := reader.Read(record[:])
		if n == 1 {
			records = append(records, record[0].(map[string]any))
		}
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return records
}

func TestPartitionArchive_ExportsEveryRowAndKeepsRecentAndDefaultPartitions(t *testing.T) {
	f := newArchiveFixture(t)
	secondSlug := provisionLedgerTenant(t, f.pool)
	second := &archiveFixture{
		schema:  "tenant_" + secondSlug,
		pool:    f.pool,
		backend: f.backend,
		month:   f.month,
	}
	if _, err := f.pool.ExecContext(t.Context(), `SELECT partman.create_partition_time($1, $2::timestamptz[])`,
		second.schema+".event_log", []time.Time{f.month}); err != nil {
		t.Fatal(err)
	}
	second.insert(t, "event_log", f.month)
	for _, table := range []string{"event_log", "audit_log"} {
		for _, at := range []time.Time{f.month, f.month.Add(time.Hour), f.month.AddDate(1, 0, 0), f.month.AddDate(-1, 0, 0)} {
			f.insert(t, table, at)
		}
	}

	if err := f.worker.Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"event_log", "audit_log"} {
		partition := f.partition(table)
		if f.attached(t, table, partition) {
			t.Errorf("%s still attached", partition)
		}
		var exists bool
		if err := f.pool.QueryRowContext(t.Context(), "SELECT to_regclass($1) IS NOT NULL", pgx.Identifier{f.schema, partition}.Sanitize()).Scan(&exists); err != nil || exists {
			t.Fatalf("partition still exists=%v, err=%v", exists, err)
		}
		records := f.archiveRows(t, f.key(table, partition))
		if len(records) != 2 {
			t.Fatalf("%s archived %d rows, want 2", table, len(records))
		}
		for _, record := range records {
			if table == "event_log" {
				if record["payload"] != "\x00\xff\x12\x34" || record["event_version"] != int64(0) || record["user_id"] != nil {
					t.Errorf("event row lost values: %#v", record)
				}
			} else {
				data, err := json.Marshal(record["new_data"], json.Deterministic(true))
				if err != nil || record["old_data"] != nil || string(data) != `{"name":"Ama","tags":[1,2]}` {
					t.Errorf("audit row lost values: %#v, err=%v", record, err)
				}
			}
		}
		for _, name := range []string{table + "_default", table + "_p" + monthSuffix(f.month.AddDate(1, 0, 0))} {
			if !f.attached(t, table, name) {
				t.Errorf("%s was removed", name)
			}
			if table == "audit_log" {
				var mutable bool
				if err := f.pool.QueryRowContext(t.Context(), "SELECT has_table_privilege('engine_user', $1, 'UPDATE, DELETE')",
					pgx.Identifier{f.schema, name}.Sanitize()).Scan(&mutable); err != nil || mutable {
					t.Fatalf("audit partition mutable=%v, err=%v", mutable, err)
				}
			}
		}
		var remaining int
		if err := f.pool.QueryRowContext(t.Context(), "SELECT count(*) FROM "+pgx.Identifier{f.schema, table}.Sanitize()).Scan(&remaining); err != nil || remaining != 2 {
			t.Fatalf("%s remaining=%d, err=%v, want 2", table, remaining, err)
		}
	}
	if records := second.archiveRows(t, second.key("event_log", second.partition("event_log"))); len(records) != 1 {
		t.Fatalf("second tenant archive rows=%d, want 1", len(records))
	}
}

func TestPartitionArchive_RetriesDDLFailuresWithoutRewriting(t *testing.T) {
	for _, tag := range []string{"ALTER TABLE", "DROP TABLE"} {
		t.Run(tag, func(t *testing.T) {
			f := newArchiveFixture(t)
			f.insert(t, "event_log", f.month)
			f.insert(t, "audit_log", f.month)
			if _, err := f.admin.ExecContext(t.Context(), `
				CREATE FUNCTION public.fail_archive_ddl() RETURNS event_trigger LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'injected archive DDL failure'; END $$;
				CREATE EVENT TRIGGER fail_archive_ddl ON ddl_command_start WHEN TAG IN ('`+tag+`')
				EXECUTE FUNCTION public.fail_archive_ddl()`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = f.admin.ExecContext(context.Background(), "DROP EVENT TRIGGER IF EXISTS fail_archive_ddl")
			})

			if err := f.worker.Work(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "injected archive DDL failure") {
				t.Fatalf("Work error=%v, want DDL failure", err)
			}
			if !f.attached(t, "audit_log", f.partition("audit_log")) {
				t.Fatal("failed detach/drop did not roll back")
			}
			if f.backend.uploads != 1 {
				t.Fatalf("uploads=%d, want 1", f.backend.uploads)
			}
			if _, err := f.admin.ExecContext(t.Context(), "DROP EVENT TRIGGER fail_archive_ddl"); err != nil {
				t.Fatal(err)
			}
			if err := f.worker.Work(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			if f.backend.uploads != 2 {
				t.Fatalf("uploads=%d, want one per old partition", f.backend.uploads)
			}
			for _, table := range []string{"event_log", "audit_log"} {
				if f.attached(t, table, f.partition(table)) {
					t.Errorf("%s remained attached after retry", table)
				}
			}
		})
	}
}

func TestPartitionArchive_StorageFailuresLeavePartitionsAttached(t *testing.T) {
	for _, operation := range []string{"exists", "upload"} {
		t.Run(operation, func(t *testing.T) {
			f := newArchiveFixture(t)
			failure := errors.New("object storage unavailable")
			if operation == "exists" {
				f.backend.existsErr = failure
			} else {
				f.backend.uploadErr = failure
			}
			if err := f.worker.Work(t.Context(), nil); !errors.Is(err, failure) {
				t.Fatalf("Work error=%v, want storage failure", err)
			}
			for _, table := range []string{"event_log", "audit_log"} {
				if !f.attached(t, table, f.partition(table)) {
					t.Errorf("%s detached after storage failure", table)
				}
			}
		})
	}
}

func TestPartitionArchive_QuotesSchemaAndPartitionIdentifiers(t *testing.T) {
	f := newArchiveFixture(t)
	f.insert(t, "event_log", f.month)
	original := f.schema
	f.schema += `"Quoted`
	if _, err := f.pool.ExecContext(t.Context(), "ALTER SCHEMA "+pgx.Identifier{original}.Sanitize()+" RENAME TO "+pgx.Identifier{f.schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.ExecContext(context.Background(), "ALTER SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" RENAME TO "+pgx.Identifier{original}.Sanitize())
	})
	name := `old "events"; partition`
	if _, err := f.pool.ExecContext(t.Context(), "ALTER TABLE "+pgx.Identifier{f.schema, f.partition("event_log")}.Sanitize()+" RENAME TO "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if records := f.archiveRows(t, f.key("event_log", name)); len(records) != 1 {
		t.Fatalf("quoted partition archive rows=%d, want 1", len(records))
	}
}

func TestPartitionArchive_RefusesAnArchiveThatDoesNotMatchThePartition(t *testing.T) {
	for _, content := range []string{"late row", "corrupt object"} {
		t.Run(content, func(t *testing.T) {
			f := newArchiveFixture(t)
			if _, err := f.pool.ExecContext(t.Context(), "CREATE VIEW "+pgx.Identifier{f.schema, "archive_dependency"}.Sanitize()+" AS SELECT * FROM "+pgx.Identifier{f.schema, f.partition("audit_log")}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			if err := f.worker.Work(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "drop partition") {
				t.Fatalf("Work error=%v, want drop failure", err)
			}
			key := f.key("audit_log", f.partition("audit_log"))
			if content == "late row" {
				f.insert(t, "audit_log", f.month)
			} else if _, err := f.backend.Backend.Upload(t.Context(), key, strings.NewReader("truncated archive"), storage.UploadOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.ExecContext(t.Context(), "DROP VIEW "+pgx.Identifier{f.schema, "archive_dependency"}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			if err := f.worker.Work(t.Context(), nil); err == nil {
				t.Fatal("Work succeeded with mismatched archive")
			}
			if !f.attached(t, "audit_log", f.partition("audit_log")) || f.backend.uploads != 1 {
				t.Fatal("mismatched archive was rewritten or partition was detached")
			}
		})
	}
}

func TestPartitionArchive_MissingBackend(t *testing.T) {
	if err := new(jobqueue.PartitionArchiveWorker).Work(t.Context(), nil); err == nil {
		t.Fatal("Work succeeded without object storage")
	}
}

func TestPartitionArchive_RetryReusesAnEventArchive(t *testing.T) {
	f := newArchiveFixture(t)
	f.insert(t, "event_log", f.month)
	if _, err := f.pool.ExecContext(t.Context(), "INSERT INTO "+pgx.Identifier{f.schema, "event_log"}.Sanitize()+`
		(event_name, emitter_module, payload, emitted_at)
		SELECT 'contacts.created', 'contacts', convert_to(n::text, 'UTF8'), $1 FROM generate_series(1, 4100) n`, f.month); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.ExecContext(t.Context(), "CREATE VIEW "+pgx.Identifier{f.schema, "archive_dependency"}.Sanitize()+" AS SELECT * FROM "+pgx.Identifier{f.schema, f.partition("event_log")}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.Work(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "drop partition") {
		t.Fatalf("Work error=%v, want drop failure", err)
	}
	if _, err := f.pool.ExecContext(t.Context(), "DROP VIEW "+pgx.Identifier{f.schema, "archive_dependency"}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if f.backend.uploads != 2 || f.attached(t, "event_log", f.partition("event_log")) {
		t.Fatal("retry rewrote the event archive or retained its partition")
	}
	if records := f.archiveRows(t, f.key("event_log", f.partition("event_log"))); len(records) != 4101 {
		t.Fatalf("multi-row-group archive rows=%d, want 4101", len(records))
	}
}
