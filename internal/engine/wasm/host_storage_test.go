package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

// hostStorageCallerModule is a hand-assembled WASM module that imports
// host.storage.upload and re-exports it as call_upload, forwarding
// (ptr, len) straight through and returning the packed i64 result
// unchanged — same technique as host_db_test.go's hostDBCallerModule.
var hostStorageCallerModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x11, 0x03, 0x60,
	0x01, 0x7F, 0x01, 0x7F, 0x60, 0x02, 0x7F, 0x7F, 0x00, 0x60, 0x02, 0x7F,
	0x7F, 0x01, 0x7E, 0x02, 0x17, 0x01, 0x0C, 0x68, 0x6F, 0x73, 0x74, 0x2E,
	0x73, 0x74, 0x6F, 0x72, 0x61, 0x67, 0x65, 0x06, 0x75, 0x70, 0x6C, 0x6F,
	0x61, 0x64, 0x00, 0x02, 0x03, 0x04, 0x03, 0x00, 0x01, 0x02, 0x05, 0x03,
	0x01, 0x00, 0x01, 0x06, 0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B,
	0x07, 0x27, 0x03, 0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65,
	0x00, 0x01, 0x0A, 0x64, 0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74,
	0x65, 0x00, 0x02, 0x0B, 0x63, 0x61, 0x6C, 0x6C, 0x5F, 0x75, 0x70, 0x6C,
	0x6F, 0x61, 0x64, 0x00, 0x03, 0x0A, 0x1F, 0x03, 0x11, 0x01, 0x01, 0x7F,
	0x23, 0x00, 0x21, 0x01, 0x20, 0x01, 0x20, 0x00, 0x6A, 0x24, 0x00, 0x20,
	0x01, 0x0B, 0x02, 0x00, 0x0B, 0x08, 0x00, 0x20, 0x00, 0x20, 0x01, 0x10,
	0x00, 0x0B,
}

func newHostStorageTestRuntime(t *testing.T, primaryDB *sql.DB, backend storage.Backend) *Runtime {
	t.Helper()

	rt, err := New(&config.Config{
		CompilationCache:    filepath.Join(t.TempDir(), "cache"),
		Environment:         string(config.Production),
		PoolMaxMemoryByes:   1 << 20,
		StorageMaxFileBytes: 100 << 20,
		StorageBlockedTypes: []string{"application/x-executable"},
	}, primaryDB, backend)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

func newHostStorageCaller(t *testing.T, ctx context.Context, r *Runtime, mc *ModuleContext) *ModuleInstance {
	t.Helper()

	compiled, err := r.wazero.CompileModule(ctx, hostStorageCallerModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("storage-caller-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	return inst
}

func newTestLocalBackend(t *testing.T) storage.Backend {
	t.Helper()
	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())

	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	return backend
}

func newHostStorageFixture(t *testing.T) (primaryDB *sql.DB, filesStore *files.Store, tenantSlug string) {
	t.Helper()
	conn := openTestPrimaryDB(t)

	slug := fmt.Sprintf("storagetest%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	store := files.NewStore(conn)
	if err := store.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("files.Store.Bootstrap: %v", err)
	}

	return conn, store, slug
}

func TestStorageUpload_CapabilityDenied(t *testing.T) {
	rt := newHostStorageTestRuntime(t, nil, nil)
	mc := newTestModuleContext("acme", 0, rt.TxLimiter())
	ctx := context.Background()
	inst := newHostStorageCaller(t, ctx, rt, mc)

	env := callHost(t, ctx, inst, "call_upload", storageUploadInput{})
	if env.OK {
		t.Fatal("expected capability_denied for a module without CapStorageWrite")
	}
	if env.Error.Code != abi.ErrCodeCapabilityDenied {
		t.Errorf("error code = %q, want %q", env.Error.Code, abi.ErrCodeCapabilityDenied)
	}
}

func TestStorageUpload_NilBackendUnavailable(t *testing.T) {
	rt := newHostStorageTestRuntime(t, nil, nil)
	mc := newTestModuleContext("acme", abi.CapStorageWrite, rt.TxLimiter())
	ctx := context.Background()
	inst := newHostStorageCaller(t, ctx, rt, mc)

	env := callHost(t, ctx, inst, "call_upload", storageUploadInput{Filename: "a.txt", ContentType: "text/plain", Data: []byte("hi")})
	if env.OK {
		t.Fatal("expected backend_unavailable for a nil storage backend")
	}
	if env.Error.Code != "storage.backend_unavailable" {
		t.Errorf("error code = %q, want %q", env.Error.Code, "storage.backend_unavailable")
	}
}

func TestStorageUpload_FileTooLarge(t *testing.T) {
	backend := newTestLocalBackend(t)
	rt := newHostStorageTestRuntime(t, nil, backend)
	mc := newTestModuleContext("acme", abi.CapStorageWrite, rt.TxLimiter())
	ctx := context.Background()
	inst := newHostStorageCaller(t, ctx, rt, mc)

	env := callHost(t, ctx, inst, "call_upload", storageUploadInput{
		Filename: "a.txt", ContentType: "text/plain", Data: []byte("hi"),
		Opts: storageUploadOpts{MaxSizeBytes: 1},
	})
	if env.OK {
		t.Fatal("expected file_too_large")
	}
	if env.Error.Code != "storage.file_too_large" {
		t.Errorf("error code = %q, want %q", env.Error.Code, "storage.file_too_large")
	}
}

func TestStorageUpload_BlockedContentType(t *testing.T) {
	backend := newTestLocalBackend(t)
	rt := newHostStorageTestRuntime(t, nil, backend)
	mc := newTestModuleContext("acme", abi.CapStorageWrite, rt.TxLimiter())
	ctx := context.Background()
	inst := newHostStorageCaller(t, ctx, rt, mc)

	env := callHost(t, ctx, inst, "call_upload", storageUploadInput{
		Filename: "a.exe", ContentType: "application/x-executable", Data: []byte("hi"),
	})
	if env.OK {
		t.Fatal("expected invalid_content_type")
	}
	if env.Error.Code != "storage.invalid_content_type" {
		t.Errorf("error code = %q, want %q", env.Error.Code, "storage.invalid_content_type")
	}
}

func TestStorageUpload_SuccessRoundTripsFileRow(t *testing.T) {
	backend := newTestLocalBackend(t)
	primaryDB, _, slug := newHostStorageFixture(t)
	rt := newHostStorageTestRuntime(t, primaryDB, backend)

	tenantID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7: %v", err)
	}
	mc := NewModuleContext("req-1", "", "", nil, tenantID.String(), slug, "trace-1", abi.CapStorageWrite, rt.TxLimiter())
	ctx := context.Background()
	inst := newHostStorageCaller(t, ctx, rt, mc)

	env := callHost(t, ctx, inst, "call_upload", storageUploadInput{
		Filename:    "invoice.pdf",
		ContentType: "application/pdf",
		Data:        []byte("%PDF-1.4 fake"),
		Opts:        storageUploadOpts{Public: true, Purpose: "attachments"},
	})
	if !env.OK {
		t.Fatalf("upload failed: %+v", env.Error)
	}

	var out storageUploadOutput
	if err := msgpack.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.FileID == "" {
		t.Error("expected a non-empty file_id")
	}
	if out.SizeBytes != int64(len("%PDF-1.4 fake")) {
		t.Errorf("size_bytes = %d, want %d", out.SizeBytes, len("%PDF-1.4 fake"))
	}
	if out.URL == "" {
		t.Error("expected a non-empty url for a public upload")
	}

	schema := tenantschema.Name(slug)
	var storageKey, contentType string
	query := fmt.Sprintf(`SELECT storage_key, content_type FROM %s.files WHERE id = $1`, schema)
	if err := primaryDB.QueryRowContext(ctx, query, out.FileID).Scan(&storageKey, &contentType); err != nil {
		t.Fatalf("query inserted files row: %v", err)
	}
	if storageKey != out.StorageKey {
		t.Errorf("files row storage_key = %q, want %q", storageKey, out.StorageKey)
	}
	if contentType != "application/pdf" {
		t.Errorf("files row content_type = %q, want %q", contentType, "application/pdf")
	}
}
