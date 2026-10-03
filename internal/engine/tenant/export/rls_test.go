package tenantexport

import (
	"archive/zip"
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func openExportRoleDB(t *testing.T, role string, bypassRLS bool) *sql.DB {
	t.Helper()

	conn, err := db.New(fmt.Sprintf("postgres://%s:dev@localhost:15432/goerp", role))
	if err != nil {
		t.Fatalf("connect as %s: %v", role, err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	var currentRole string
	var superuser, bypass bool
	err = conn.QueryRowContext(t.Context(), `
		SELECT current_user, rolsuper, rolbypassrls
		FROM pg_roles WHERE rolname = current_user
	`).Scan(&currentRole, &superuser, &bypass)
	if err != nil {
		t.Fatalf("inspect database identity: %v", err)
	}

	if currentRole != role || superuser || bypass != bypassRLS {
		t.Fatalf("database identity = %s, superuser=%t, bypassrls=%t; want %s, false, %t",
			currentRole, superuser, bypass, role, bypassRLS)
	}

	return conn
}

func TestWorkerRun_ExportsEveryRLSProtectedRowAsSchemaSyncUser(t *testing.T) {
	ctx := t.Context()
	f := newExportTestFixture(t)
	adminDB := f.worker.RawDB
	schema := tenantschema.Name(f.tenantSlug)

	expected := map[string]string{
		"11111111-1111-1111-1111-111111111111": "Widget A",
		"22222222-2222-2222-2222-222222222222": "Widget B",
	}

	_, err := adminDB.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s.widgets (id, name, credit_limit)
		VALUES ('22222222-2222-2222-2222-222222222222', 'Widget B', 7000);
		ALTER TABLE %s.widgets ENABLE ROW LEVEL SECURITY;
		ALTER TABLE %s.widgets FORCE ROW LEVEL SECURITY;
		CREATE POLICY own_widget ON %s.widgets
			USING (id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
		GRANT USAGE ON SCHEMA %s TO engine_user, schema_sync_user;
		GRANT SELECT ON %s.widgets TO engine_user, schema_sync_user;
	`, schema, schema, schema, schema, schema, schema))
	if err != nil {
		t.Fatalf("protect export fixture with RLS: %v", err)
	}

	primaryDB := openExportRoleDB(t, "engine_user", false)
	tx, err := beginTenantScopedRead(ctx, primaryDB, f.tenantSlug)
	if err != nil {
		t.Fatalf("begin ordinary engine read: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	var visible int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM widgets").Scan(&visible); err != nil {
		t.Fatalf("read without acting user: %v", err)
	}

	if visible != 0 {
		t.Fatalf("rows visible without acting user = %d, want 0", visible)
	}

	_, err = tx.ExecContext(ctx, "SELECT set_config('app.current_user_id', $1, true)",
		"11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatalf("set acting user: %v", err)
	}

	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM widgets").Scan(&visible); err != nil {
		t.Fatalf("read as acting user: %v", err)
	}

	if visible != 1 {
		t.Fatalf("rows visible to acting user = %d, want 1", visible)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("finish ordinary engine read: %v", err)
	}

	f.worker.RawDB = openExportRoleDB(t, "schema_sync_user", true)
	jobID := time.Now().UnixNano()
	t.Cleanup(func() {
		_, _ = adminDB.Exec("DELETE FROM system.job_checkpoints WHERE job_id = $1", fmt.Sprint(jobID))
	})

	result, err := f.worker.run(ctx, testJob(jobID, Args{TenantID: f.tenantID, TenantSlug: f.tenantSlug}))
	if err != nil {
		t.Fatalf("export as schema_sync_user: %v", err)
	}

	archiveKey := fmt.Sprintf("exports/%s/%d/archive.zip.enc", f.tenantID, jobID)
	object, _, err := f.worker.StorageBackend.Download(ctx, archiveKey)
	if err != nil {
		t.Fatalf("download encrypted archive: %v", err)
	}
	defer func() { _ = object.Close() }()

	ciphertext, err := io.ReadAll(object)
	if err != nil {
		t.Fatalf("read encrypted archive: %v", err)
	}

	key, err := f.keys.Decrypt([]byte(result.DecryptionKey))
	if err != nil {
		t.Fatalf("decrypt archive key: %v", err)
	}

	plaintext := decryptArchive(t, ciphertext, string(key))
	archive, err := zip.NewReader(bytes.NewReader(plaintext), int64(len(plaintext)))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}

	rows, err := archive.Open("testmodule.jsonl")
	if err != nil {
		t.Fatalf("open exported rows: %v", err)
	}
	defer func() { _ = rows.Close() }()

	got := make(map[string]string)
	scanner := bufio.NewScanner(rows)
	for scanner.Scan() {
		var record exportRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode exported row: %v", err)
		}

		id, validID := record.Record["id"].(string)
		name, validName := record.Record["name"].(string)
		if record.Model != "widget" || !validID || !validName {
			t.Fatalf("unexpected exported record: %+v", record)
		}

		if _, duplicate := got[id]; duplicate {
			t.Fatalf("duplicate exported row %s", id)
		}

		if _, restricted := record.Record["credit_limit"]; restricted {
			t.Fatalf("restricted credit_limit field exported for row %s", id)
		}

		got[id] = name
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("read exported rows: %v", err)
	}

	if !maps.Equal(got, expected) {
		t.Fatalf("exported rows = %v, want %v", got, expected)
	}
}
