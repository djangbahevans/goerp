package workflowworker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/temporal"
)

func TestVerifyChecksum(t *testing.T) {
	data := []byte("hello world")
	sum := sha256.Sum256(data)
	good := "sha256:" + hex.EncodeToString(sum[:])

	if err := verifyChecksum(good, data); err != nil {
		t.Errorf("verifyChecksum() with a matching checksum: %v", err)
	}
	if err := verifyChecksum("sha256:deadbeef", data); err == nil {
		t.Error("verifyChecksum() with a mismatched checksum: expected an error, got nil")
	}
	if err := verifyChecksum("md5:abc", data); err == nil {
		t.Error("verifyChecksum() with a non-sha256 checksum: expected an error, got nil")
	}
}

func newLocalStorage(t *testing.T) storage.Backend {
	t.Helper()
	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New(local): %v", err)
	}
	return backend
}

func TestFetchAndVerify(t *testing.T) {
	backend := newLocalStorage(t)
	m := NewManager(backend, nil, t.TempDir())

	data := []byte("fake workflow-worker binary")
	sum := sha256.Sum256(data)
	checksum := "sha256:" + hex.EncodeToString(sum[:])
	if _, err := backend.Upload(context.Background(), checksum, bytes.NewReader(data), storage.UploadOptions{}); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	mf := manifest.Manifest{Name: "testmod", WorkerChecksum: checksum}

	binPath, err := m.fetchAndVerify(context.Background(), mf)
	if err != nil {
		t.Fatalf("fetchAndVerify() error: %v", err)
	}
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read cached binary: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("cached binary content = %q, want %q", got, data)
	}
}

func TestFetchAndVerifyChecksumMismatch(t *testing.T) {
	backend := newLocalStorage(t)
	m := NewManager(backend, nil, t.TempDir())

	data := []byte("fake workflow-worker binary")
	wrongChecksum := "sha256:" + hex.EncodeToString(sha256.New().Sum(nil)) // checksum of empty, not data
	if _, err := backend.Upload(context.Background(), wrongChecksum, bytes.NewReader(data), storage.UploadOptions{}); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	mf := manifest.Manifest{Name: "testmod", WorkerChecksum: wrongChecksum}
	if _, err := m.fetchAndVerify(context.Background(), mf); err == nil {
		t.Error("fetchAndVerify() with mismatched checksum: expected an error, got nil")
	}
}

func TestSpawnAllSkipsModulesWithoutWorkflows(t *testing.T) {
	m := NewManager(nil, nil, t.TempDir())
	mods := map[string]*module.LoadedModule{
		"nomod": {Manifest: manifest.Manifest{Name: "nomod"}},
	}
	if err := m.SpawnAll(context.Background(), mods); err != nil {
		t.Errorf("SpawnAll() for a module with no workflow types: error = %v, want nil", err)
	}
	if m.Validate("anything", "nomod") {
		t.Error("Validate() true for a module SpawnAll never spawned")
	}
}

// TestSpawnAllReturnsErrorOnBadDependencies guards the fail-hard contract:
// unlike Stage 3/4, a workflow-worker spawn failure must propagate as an
// error rather than degrade to module.StatusFailed and continue —
// engine-internals.md §2's startup sequence gets no per-module carve-out
// for Stage 6.
func TestSpawnAllReturnsErrorOnBadDependencies(t *testing.T) {
	m := NewManager(nil, nil, t.TempDir()) // nil storage/temporal
	mods := map[string]*module.LoadedModule{
		"wf": {Manifest: manifest.Manifest{
			Name:           "wf",
			WorkerChecksum: "sha256:00",
			WorkflowTypes:  []manifest.WorkflowType{{Name: "x"}},
		}},
	}
	err := m.SpawnAll(context.Background(), mods)
	if err == nil {
		t.Fatal("SpawnAll() with no storage/temporal backend: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "wf") {
		t.Errorf("SpawnAll() error = %q, want it to name the failed module (%q)", err.Error(), "wf")
	}
	if mods["wf"].Status == module.StatusFailed {
		t.Error("SpawnAll() set module.StatusFailed — a fail-hard error shouldn't be conflated with Stage 3/4's per-module degrade status")
	}
}

// TestSpawnAllAttemptsEveryModuleBeforeReturning confirms one module's
// failure doesn't short-circuit the others — SpawnAll reports every
// failure in a single joined error rather than stopping at the first.
func TestSpawnAllAttemptsEveryModuleBeforeReturning(t *testing.T) {
	m := NewManager(nil, nil, t.TempDir()) // nil storage/temporal: every module fails
	mods := map[string]*module.LoadedModule{
		"wf-a": {Manifest: manifest.Manifest{Name: "wf-a", WorkerChecksum: "sha256:00", WorkflowTypes: []manifest.WorkflowType{{Name: "x"}}}},
		"wf-b": {Manifest: manifest.Manifest{Name: "wf-b", WorkerChecksum: "sha256:00", WorkflowTypes: []manifest.WorkflowType{{Name: "x"}}}},
	}
	err := m.SpawnAll(context.Background(), mods)
	if err == nil {
		t.Fatal("SpawnAll() with no storage/temporal backend: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "wf-a") || !strings.Contains(err.Error(), "wf-b") {
		t.Errorf("SpawnAll() error = %q, want it to name both failed modules", err.Error())
	}
}

// buildTestWorker compiles the testdata/testworker helper (a minimal
// Temporal worker that polls goerp:{GOERP_WORKFLOW_WORKER_MODULE}) and
// returns the path to the resulting binary.
func buildTestWorker(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "testworker")
	cmd := exec.Command("go", "build", "-o", out, "./testdata/testworker")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build testworker: %v\n%s", err, output)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read built testworker: %v", err)
	}
	return data
}

func newTestTemporalClient(t *testing.T) *temporal.Client {
	t.Helper()
	t.Setenv("GOERP_TEMPORAL_HOST_PORT", "127.0.0.1:7233")
	t.Setenv("GOERP_TEMPORAL_NAMESPACE", "default")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := temporal.New(ctx)
	if err != nil {
		t.Skipf("temporal not reachable at 127.0.0.1:7233 (start compose.dev.yml): %v", err)
	}
	return c
}

// TestSpawnConfirmAndStopAll exercises the full pipeline against real
// infrastructure (compose.dev.yml's Temporal, a local-disk storage
// backend, and an actual child OS process) rather than mocking any of it:
// download+verify the binary, spawn it, confirm it registered a poller,
// authorize its credential, then stop it and confirm the credential is
// revoked.
func TestSpawnConfirmAndStopAll(t *testing.T) {
	temporalClient := newTestTemporalClient(t)
	defer temporalClient.Close()

	data := buildTestWorker(t)
	sum := sha256.Sum256(data)
	checksum := "sha256:" + hex.EncodeToString(sum[:])

	backend := newLocalStorage(t)
	if _, err := backend.Upload(context.Background(), checksum, bytes.NewReader(data), storage.UploadOptions{}); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	m := NewManager(backend, temporalClient, t.TempDir())
	mod := &module.LoadedModule{Manifest: manifest.Manifest{
		Name:           "spawntest",
		WorkerChecksum: checksum,
		WorkflowTypes:  []manifest.WorkflowType{{Name: "x"}},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), pollerConfirmTimeout+10*time.Second)
	defer cancel()

	if err := m.spawn(ctx, mod); err != nil {
		t.Fatalf("spawn() error: %v", err)
	}

	m.mu.Lock()
	p, ok := m.processes["spawntest"]
	m.mu.Unlock()
	if !ok {
		t.Fatal("spawn() succeeded but process not tracked")
	}

	if !m.Validate(p.credential.Token, "spawntest") {
		t.Error("Validate() false for the token spawn() just minted")
	}
	if m.Validate("wrong-token", "spawntest") {
		t.Error("Validate() true for a token that was never minted")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	m.StopAll(stopCtx)

	if m.Validate(p.credential.Token, "spawntest") {
		t.Error("Validate() true after StopAll() — credential should be revoked")
	}
}
