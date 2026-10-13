package jobqueue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type cleanupBackend struct {
	storage.Backend
	failKey string
	calls   map[string]int
}

func (b *cleanupBackend) Delete(ctx context.Context, key string) error {
	b.calls[key]++
	if key == b.failKey {
		return errors.New("backend unavailable")
	}

	return b.Backend.Delete(ctx, key)
}

func newStorageCleanupFixture(t *testing.T) (*StorageCleanupWorker, *sql.DB, *cleanupBackend) {
	t.Helper()
	admin, err := db.New(inviteExpiryTestDSN)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	// ActiveTenants enumerates the whole database; destructive sweeps need a
	// private database so shared tenant files cannot become test candidates.
	database := fmt.Sprintf("storagecleanup%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+database+" WITH (FORCE)"); err != nil {
			t.Errorf("drop fixture database: %v", err)
		}
	})

	conn, err := db.New("postgres://goerp:dev@localhost:15432/" + database)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	tenants := tenant.NewStore(conn)
	if err := tenants.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, err := storage.New("local")
	if err != nil {
		t.Fatal(err)
	}
	b := &cleanupBackend{Backend: backend, calls: make(map[string]int)}
	w := &StorageCleanupWorker{
		TenantStore: tenants,
		FileStore:   files.NewStore(conn),
		Backend:     b,
		Retention:   24 * time.Hour,
	}

	return w, conn, b
}

func cleanupTenantFixture(t *testing.T, w *StorageCleanupWorker, conn *sql.DB, slug string, status tenant.Status) *tenant.Tenant {
	t.Helper()
	tt, err := w.TenantStore.CreateTenant(t.Context(), slug, slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.TenantStore.UpdateStatus(t.Context(), slug, status, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "CREATE SCHEMA "+tenantschema.Name(slug)); err != nil {
		t.Fatal(err)
	}
	if err := w.FileStore.Bootstrap(t.Context(), slug); err != nil {
		t.Fatal(err)
	}

	return tt
}

func cleanupFileFixture(t *testing.T, w *StorageCleanupWorker, conn *sql.DB, tt *tenant.Tenant, age time.Duration) files.InsertRow {
	t.Helper()
	id := uuid.NewV7().String()
	row := files.InsertRow{
		ID:           id,
		TenantID:     tt.ID,
		StorageKey:   "attachments/" + tt.ID + "/" + id,
		OriginalName: "file.txt",
		ContentType:  "text/plain",
		SizeBytes:    4,
		Purpose:      "attachments",
	}
	if _, err := w.Backend.Upload(t.Context(), row.StorageKey, strings.NewReader("file"), storage.UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := w.FileStore.Insert(t.Context(), tt.Slug, row); err != nil {
		t.Fatal(err)
	}
	if age > 0 {
		query := "UPDATE " + tenantschema.Name(tt.Slug) + ".files SET deleted_at = $1 WHERE id = $2"
		if _, err := conn.ExecContext(t.Context(), query, time.Now().Add(-age), id); err != nil {
			t.Fatal(err)
		}
	}

	return row
}

func assertCleanupFile(t *testing.T, w *StorageCleanupWorker, slug string, row files.InsertRow, wantRow, wantObject bool) {
	t.Helper()
	_, err := w.FileStore.GetByID(t.Context(), slug, row.ID)
	if wantRow && err != nil || !wantRow && !errors.Is(err, files.ErrFileNotFound) {
		t.Errorf("file %s: want row %v, error %v", row.ID, wantRow, err)
	}
	exists, err := w.Backend.Exists(t.Context(), row.StorageKey)
	if err != nil || exists != wantObject {
		t.Errorf("file %s: object exists %v, want %v, error %v", row.ID, exists, wantObject, err)
	}
}

func TestStorageCleanupWorker_RetentionAndRetry(t *testing.T) {
	w, conn, backend := newStorageCleanupFixture(t)
	tt := cleanupTenantFixture(t, w, conn, "cleanup", tenant.StatusActive)
	failed := cleanupFileFixture(t, w, conn, tt, 48*time.Hour)
	backend.failKey = failed.StorageKey
	old := cleanupFileFixture(t, w, conn, tt, 48*time.Hour)
	recent := cleanupFileFixture(t, w, conn, tt, 23*time.Hour)
	active := cleanupFileFixture(t, w, conn, tt, 0)
	missing := cleanupFileFixture(t, w, conn, tt, 48*time.Hour)
	if err := backend.Backend.Delete(t.Context(), missing.StorageKey); err != nil {
		t.Fatal(err)
	}
	suspended := cleanupTenantFixture(t, w, conn, "suspended", tenant.StatusSuspended)
	suspendedFile := cleanupFileFixture(t, w, conn, suspended, 48*time.Hour)

	if err := w.Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	assertCleanupFile(t, w, tt.Slug, failed, true, true)
	assertCleanupFile(t, w, tt.Slug, old, false, false)
	assertCleanupFile(t, w, tt.Slug, recent, true, true)
	assertCleanupFile(t, w, tt.Slug, active, true, true)
	assertCleanupFile(t, w, tt.Slug, missing, false, false)
	assertCleanupFile(t, w, suspended.Slug, suspendedFile, true, true)
	if backend.calls[failed.StorageKey] != 1 || backend.calls[active.StorageKey] != 0 || backend.calls[recent.StorageKey] != 0 || backend.calls[suspendedFile.StorageKey] != 0 {
		t.Fatalf("unexpected deletion attempts: %v", backend.calls)
	}

	backend.failKey = ""
	if err := w.Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	assertCleanupFile(t, w, tt.Slug, failed, false, false)
	if backend.calls[failed.StorageKey] != 2 {
		t.Errorf("failed file deletion attempts = %d, want 2", backend.calls[failed.StorageKey])
	}
}

func TestStorageCleanupWorker_TenantFailureAndPagination(t *testing.T) {
	w, conn, backend := newStorageCleanupFixture(t)
	broken := cleanupTenantFixture(t, w, conn, "aaa", tenant.StatusActive)
	if _, err := conn.ExecContext(t.Context(), "ALTER TABLE "+tenantschema.Name(broken.Slug)+".files DROP COLUMN deleted_at"); err != nil {
		t.Fatal(err)
	}
	tt := cleanupTenantFixture(t, w, conn, "zzz", tenant.StatusActive)
	failed := cleanupFileFixture(t, w, conn, tt, 48*time.Hour)
	backend.failKey = failed.StorageKey
	for range 105 {
		cleanupFileFixture(t, w, conn, tt, 48*time.Hour)
	}

	if err := w.Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := conn.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+tenantschema.Name(tt.Slug)+".files").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 || len(backend.calls) != 106 || backend.calls[failed.StorageKey] != 1 {
		t.Fatalf("remaining rows = %d, delete attempts = %v", remaining, backend.calls)
	}
	assertCleanupFile(t, w, tt.Slug, failed, true, true)
}

func TestStorageCleanupWorker_MetadataFailureRetriesMissingObject(t *testing.T) {
	w, conn, _ := newStorageCleanupFixture(t)
	tt := cleanupTenantFixture(t, w, conn, "cleanup", tenant.StatusActive)
	failed := cleanupFileFixture(t, w, conn, tt, 48*time.Hour)
	other := cleanupFileFixture(t, w, conn, tt, 48*time.Hour)
	schema := tenantschema.Name(tt.Slug)
	query := fmt.Sprintf(`CREATE FUNCTION %s.reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF OLD.id = '%s'::uuid THEN RAISE EXCEPTION 'metadata unavailable'; END IF; RETURN OLD; END $$`, schema, failed.ID)
	if _, err := conn.ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "CREATE TRIGGER reject_delete BEFORE DELETE ON "+schema+".files FOR EACH ROW EXECUTE FUNCTION "+schema+".reject_delete()"); err != nil {
		t.Fatal(err)
	}

	if err := w.Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	assertCleanupFile(t, w, tt.Slug, failed, true, false)
	assertCleanupFile(t, w, tt.Slug, other, false, false)
	if _, err := conn.ExecContext(t.Context(), "DROP TRIGGER reject_delete ON "+schema+".files"); err != nil {
		t.Fatal(err)
	}
	if err := w.Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	assertCleanupFile(t, w, tt.Slug, failed, false, false)
}

func TestStorageCleanupWorker_UnconfiguredBackend(t *testing.T) {
	w := &StorageCleanupWorker{}
	if err := w.Work(t.Context(), nil); err == nil {
		t.Fatal("cleanup accepted an unconfigured backend")
	}
}

func TestStorageCleanupWorker_DefaultRetentionAndCancellation(t *testing.T) {
	w, conn, backend := newStorageCleanupFixture(t)
	w.Retention = 0
	tt := cleanupTenantFixture(t, w, conn, "cleanup", tenant.StatusActive)
	old := cleanupFileFixture(t, w, conn, tt, 8*24*time.Hour)
	recent := cleanupFileFixture(t, w, conn, tt, 6*24*time.Hour)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.Work(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled cleanup error = %v", err)
	}
	if len(backend.calls) != 0 {
		t.Fatal("cancelled cleanup attempted backend deletion")
	}

	if err := w.Work(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	assertCleanupFile(t, w, tt.Slug, old, false, false)
	assertCleanupFile(t, w, tt.Slug, recent, true, true)
}
