package wasm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/vmihailenco/msgpack/v5"
)

// Compiled Go modules linking storage and MessagePack need a larger memory cap than hand-
// written WASM fixtures.
func newStorageHostcallTestRuntime(t *testing.T, primaryDB *sql.DB, backend storage.Backend) *Runtime {
	t.Helper()

	rt, err := New(&config.Config{
		CompilationCache:    wasmtest.SharedCompilationCacheDir(),
		Environment:         string(config.Production),
		PoolMaxMemoryByes:   8 << 20,
		StorageMaxFileBytes: 100 << 20,
		StorageBlockedTypes: []string{"application/x-executable"},
	}, primaryDB, backend, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

func compileStorageCallerFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "storagecallerfixture.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/storagecallerfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/storagecallerfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}

	wasmtest.Precompile(t, data, wasmtest.SharedCompilationCacheDir())

	return data
}

type storageResult struct {
	Metadata       abiv1.FileMetadata `msgpack:"metadata"`
	URL            string             `msgpack:"url"`
	PublicURL      string             `msgpack:"public_url"`
	OK             bool               `msgpack:"ok"`
	Error          string             `msgpack:"error,omitempty"`
	FileID         string             `msgpack:"file_id,omitempty"`
	StorageKey     string             `msgpack:"storage_key,omitempty"`
	SizeBytes      int64              `msgpack:"size_bytes,omitempty"`
	ChecksumSHA256 string             `msgpack:"checksum_sha256,omitempty"`
}

func TestStorageCallerFixture_Upload_RoundTripsThroughRealModule(t *testing.T) {
	primaryDB, _, slug := newHostStorageFixture(t)
	backend := newTestLocalBackend(t)
	ctx := t.Context()
	wasmBytes := compileStorageCallerFixture(t)

	tenantID := uuid.NewV7()
	mc := NewModuleContext("req-1", "testmodule", "", "", nil, nil, tenantID.String(), slug, "trace-1", abi.CapStorageWrite|abi.CapStorageRead, nil, ModuleSnapshot{})
	r := newStorageHostcallTestRuntime(t, primaryDB, backend)

	compiled, err := r.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("storagecallerfixture-%d", time.Now().UnixNano()), compiled, r)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	fn := inst.module.ExportedFunction("run_upload")
	if fn == nil {
		t.Fatal("fixture has no export run_upload")
	}
	results, err := fn.Call(ctx)
	if err != nil {
		t.Fatalf("call run_upload: %v", err)
	}

	packed := results[0]
	ptr := uint32(packed >> 32)
	length := uint32(packed)
	raw, ok := inst.module.Memory().Read(ptr, length)
	if !ok {
		t.Fatalf("read result at ptr=%d len=%d: out of bounds", ptr, length)
	}

	var out storageResult
	if err := msgpack.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal storageResult: %v", err)
	}
	if !out.OK {
		t.Fatalf("run_upload failed: %s", out.Error)
	}

	wantData := []byte("hello from a real wasip1 module")
	wantSum := sha256.Sum256(wantData)
	wantChecksum := hex.EncodeToString(wantSum[:])

	if out.FileID == "" {
		t.Error("expected a non-empty FileID")
	}
	if out.StorageKey == "" {
		t.Error("expected a non-empty StorageKey")
	}
	if out.SizeBytes != int64(len(wantData)) {
		t.Errorf("SizeBytes = %d, want %d", out.SizeBytes, len(wantData))
	}
	if out.ChecksumSHA256 != wantChecksum {
		t.Errorf("ChecksumSHA256 = %q, want %q", out.ChecksumSHA256, wantChecksum)
	}
}

func TestStorageCallerFixture_Lifecycle(t *testing.T) {
	primaryDB, store, slug := newHostStorageFixture(t)
	backend := newTestLocalBackend(t)
	rt := newStorageHostcallTestRuntime(t, primaryDB, backend)
	ctx := t.Context()
	compiled, err := rt.wazero.CompileModule(ctx, compileStorageCallerFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	inst, err := newModuleInstance(ctx, "storage-sdk-lifecycle", compiled, rt)
	if err != nil {
		t.Fatal(err)
	}
	tenantID, userID := uuid.NewV7().String(), uuid.NewV7().String()
	mc := NewModuleContext("req", "storage", userID, "", nil, nil, tenantID, slug, "trace", abi.CapStorageRead|abi.CapStorageWrite, rt.TxLimiter(), ModuleSnapshot{})
	inst.SetModuleContext(mc)
	rt.RegisterInstance(inst)
	t.Cleanup(func() { rt.UnregisterInstance(inst) })

	results, err := inst.module.ExportedFunction("run_lifecycle").Call(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := inst.module.Memory().Read(uint32(results[0]>>32), uint32(results[0]))
	if !ok {
		t.Fatal("read lifecycle result")
	}
	var out storageResult
	if err := msgpack.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("SDK lifecycle: %s", out.Error)
	}
	row, err := store.GetByID(ctx, slug, out.FileID)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256([]byte("private file"))
	want := abiv1.FileMetadata{
		ID:           out.FileID,
		OriginalName: "private.txt",
		ContentType:  "text/plain",
		SizeBytes:    12,
		Checksum:     hex.EncodeToString(checksum[:]),
		UploadedBy:   userID,
		Purpose:      "attachments",
		ScanStatus:   "pending",
		CreatedAt:    row.CreatedAt,
	}
	if out.Metadata != want || out.Metadata.CreatedAt.IsZero() || out.URL == "" || out.PublicURL == "" || row.DeletedAt == nil {
		t.Fatalf("SDK result: %+v, row: %+v", out, row)
	}
	exists, err := backend.Exists(ctx, out.StorageKey)
	if err != nil || !exists {
		t.Fatalf("object after SDK delete: %v, %v", exists, err)
	}
}
