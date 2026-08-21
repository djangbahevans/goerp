package files

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/google/uuid"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/role's/tenant's/user's
// tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// openTestStore creates a fixture tenant_<random> schema directly (this
// package's tests don't wait on real tenant provisioning to exist — same
// convention as role_test.go's openTestStore) and returns a Store plus
// that schema's slug for tests to target.
func openTestStore(t *testing.T) (store *Store, conn *sql.DB, tenantSlug string) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug := fmt.Sprintf("filestest%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)

	if _, err := conn.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	store = NewStore(conn)
	if err := store.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return store, conn, slug
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, _, slug := openTestStore(t)

	if err := store.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

func TestInsert_RoundTripsRow(t *testing.T) {
	store, conn, slug := openTestStore(t)
	ctx := context.Background()

	fileID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7: %v", err)
	}
	tenantID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7: %v", err)
	}

	row := InsertRow{
		ID:             fileID.String(),
		TenantID:       tenantID.String(),
		StorageKey:     "attachments/" + tenantID.String() + "/2026/08/" + fileID.String() + ".pdf",
		OriginalName:   "invoice.pdf",
		ContentType:    "application/pdf",
		SizeBytes:      1234,
		ChecksumSHA256: "deadbeef",
		UploadedBy:     "",
		Purpose:        "attachments",
		IsPublic:       false,
	}

	if err := store.Insert(ctx, slug, row); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}

	schema := tenantschema.Name(slug)
	var (
		storageKey  string
		contentType string
		sizeBytes   int64
		scanStatus  string
		uploadedBy  sql.NullString
	)
	query := fmt.Sprintf(`SELECT storage_key, content_type, size_bytes, scan_status, uploaded_by FROM %s.files WHERE id = $1`, schema)
	if err := conn.QueryRowContext(ctx, query, row.ID).Scan(&storageKey, &contentType, &sizeBytes, &scanStatus, &uploadedBy); err != nil {
		t.Fatalf("query inserted row: %v", err)
	}

	if storageKey != row.StorageKey {
		t.Errorf("storage_key = %q, want %q", storageKey, row.StorageKey)
	}
	if contentType != row.ContentType {
		t.Errorf("content_type = %q, want %q", contentType, row.ContentType)
	}
	if sizeBytes != row.SizeBytes {
		t.Errorf("size_bytes = %d, want %d", sizeBytes, row.SizeBytes)
	}
	if scanStatus != "pending" {
		t.Errorf("scan_status = %q, want %q", scanStatus, "pending")
	}
	if uploadedBy.Valid {
		t.Errorf("expected uploaded_by to be NULL for an empty UploadedBy, got %q", uploadedBy.String)
	}
}

func TestInsert_DuplicateStorageKeyFails(t *testing.T) {
	store, _, slug := openTestStore(t)
	ctx := context.Background()

	fileID, _ := uuid.NewV7()
	tenantID, _ := uuid.NewV7()
	row := InsertRow{
		ID:           fileID.String(),
		TenantID:     tenantID.String(),
		StorageKey:   "attachments/dup-key",
		OriginalName: "a.txt",
		ContentType:  "text/plain",
		SizeBytes:    1,
		Purpose:      "attachments",
	}
	if err := store.Insert(ctx, slug, row); err != nil {
		t.Fatalf("first Insert() error: %v", err)
	}

	secondID, _ := uuid.NewV7()
	row.ID = secondID.String()
	if err := store.Insert(ctx, slug, row); err == nil {
		t.Fatal("expected a duplicate storage_key to fail (UNIQUE constraint)")
	}
}

func TestStorageKeysForTenant_ReturnsEveryInsertedKey(t *testing.T) {
	store, _, slug := openTestStore(t)
	ctx := context.Background()

	tenantID, _ := uuid.NewV7()
	want := make(map[string]bool)
	for i := range 3 {
		fileID, _ := uuid.NewV7()
		key := fmt.Sprintf("attachments/%s/2026/08/%s-%d.txt", tenantID.String(), fileID.String(), i)
		if err := store.Insert(ctx, slug, InsertRow{
			ID: fileID.String(), TenantID: tenantID.String(), StorageKey: key,
			OriginalName: "f.txt", ContentType: "text/plain", SizeBytes: 1, Purpose: "attachments",
		}); err != nil {
			t.Fatalf("Insert() error: %v", err)
		}
		want[key] = true
	}

	got, err := store.StorageKeysForTenant(ctx, slug)
	if err != nil {
		t.Fatalf("StorageKeysForTenant() error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for _, key := range got {
		if !want[key] {
			t.Errorf("unexpected key %q", key)
		}
	}
}

func TestStorageKeysForTenant_NoFilesTableReturnsEmpty(t *testing.T) {
	store, conn, slug := openTestStore(t)
	ctx := context.Background()

	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("DROP TABLE %s.files", schema)); err != nil {
		t.Fatalf("drop files table: %v", err)
	}

	got, err := store.StorageKeysForTenant(ctx, slug)
	if err != nil {
		t.Fatalf("StorageKeysForTenant() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}
