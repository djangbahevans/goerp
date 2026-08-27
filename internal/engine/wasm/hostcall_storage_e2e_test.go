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

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

// newStorageHostcallTestRuntime is newHostStorageTestRuntime with a
// larger memory cap — see newHostcallTestRuntime's own doc comment
// (hostcall_e2e_test.go) for why a real module linking in
// sdk/go/storage/msgpack needs more than 1 MiB.
func newStorageHostcallTestRuntime(t *testing.T, primaryDB *sql.DB, backend storage.Backend) *Runtime {
	t.Helper()

	rt, err := New(&config.Config{
		CompilationCache:    filepath.Join(t.TempDir(), "cache"),
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

// compileStorageCallerFixture compiles testdata/storagecallerfixture —
// a real module built on the actual sdk/go/storage package, not
// hand-assembled bytecode — to wasip1 WASM, the same way
// compileHostcallFixture (hostcall_e2e_test.go) compiles
// testdata/hostcallfixture. Proves goerp#434's acceptance criterion: a
// real compiled module can upload a file via the SDK wrapper against a
// real engine instance and get back the decoded storageUploadOutput.
func compileStorageCallerFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "storagecallerfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/storagecallerfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/storagecallerfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// storageResult mirrors testdata/storagecallerfixture's own result
// envelope by field name and msgpack tag.
type storageResult struct {
	OK             bool   `msgpack:"ok"`
	Error          string `msgpack:"error,omitempty"`
	FileID         string `msgpack:"file_id,omitempty"`
	StorageKey     string `msgpack:"storage_key,omitempty"`
	SizeBytes      int64  `msgpack:"size_bytes,omitempty"`
	ChecksumSHA256 string `msgpack:"checksum_sha256,omitempty"`
}

func TestStorageCallerFixture_Upload_RoundTripsThroughRealModule(t *testing.T) {
	primaryDB, _, slug := newHostStorageFixture(t)
	backend := newTestLocalBackend(t)
	ctx := context.Background()
	wasmBytes := compileStorageCallerFixture(t)

	tenantID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7: %v", err)
	}
	mc := NewModuleContext("req-1", "testmodule", "", "", nil, nil, tenantID.String(), slug, "trace-1", abi.CapStorageWrite, nil, ModuleSnapshot{})
	r := newStorageHostcallTestRuntime(t, primaryDB, backend)

	compiled, err := r.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("storagecallerfixture-%d", time.Now().UnixNano()), compiled, r.wazero)
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
