package module

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const wasmFixtureManifestBundle = `{
  "abi_version": "1",
  "capabilities": [],
  "checksum": "sha256:empty",
  "depends_on": [],
  "description": "fixture module",
  "display_name": "Fixture",
  "engine": ">=0.1.0",
  "name": "fixture",
  "schema": {
    "owned_models": []
  },
  "type": "domain",
  "version": "0.1.0"
}
`

const wasmFixtureManifestNoWasm = `{
  "abi_version": "1",
  "capabilities": [],
  "checksum": "sha256:empty",
  "depends_on": [],
  "description": "fixture module",
  "display_name": "Fixture",
  "engine": ">=0.1.0",
  "name": "fixture",
  "schema": {
    "owned_models": []
  },
  "type": "domain",
  "version": "0.1.0",
  "wasm": false
}
`

const wasmFixtureMainGo = `package main

func main() {}
`

const wasmFixtureGoMod = `module goerp-wasmbuild-fixture

go 1.26.5
`

func writeWasmFixture(t *testing.T, dir, manifestJSON string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(wasmFixtureGoMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	cmdModuleDir := filepath.Join(dir, "cmd", "module")
	if err := os.MkdirAll(cmdModuleDir, 0o755); err != nil {
		t.Fatalf("mkdir cmd/module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdModuleDir, "main.go"), []byte(wasmFixtureMainGo), 0o644); err != nil {
		t.Fatalf("write cmd/module/main.go: %v", err)
	}
}

func TestBuildWasmCompilesReactorBinary(t *testing.T) {
	dir := t.TempDir()
	writeWasmFixture(t, dir, wasmFixtureManifestBundle)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := BuildWasm(ctx, dir, false)
	if err != nil {
		t.Fatalf("BuildWasm: %v", err)
	}
	if result == nil {
		t.Fatal("BuildWasm returned nil result for a module without wasm: false")
	}

	data, err := os.ReadFile(result.WasmPath)
	if err != nil {
		t.Fatalf("read wasm output: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("module.wasm is empty")
	}

	sum := sha256.Sum256(data)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if result.WasmSHA256 != want {
		t.Errorf("WasmSHA256 = %q, want %q", result.WasmSHA256, want)
	}
}

// TestBuildWasmWorksWithRelativeDir guards against a real regression: -o
// was previously joined with dir before being passed to a subprocess that
// also had cmd.Dir set to dir, double-resolving the path (e.g.
// "modules/demo" + cmd.Dir="modules/demo" produced
// "modules/demo/modules/demo/module.wasm"). t.TempDir() always returns an
// absolute path, which masked this — an absolute -o value isn't affected
// by cmd.Dir, so this test deliberately uses a relative dir instead.
func TestBuildWasmWorksWithRelativeDir(t *testing.T) {
	t.Chdir(t.TempDir())

	relDir := "fixture-module"
	if err := os.Mkdir(relDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relDir, err)
	}
	writeWasmFixture(t, relDir, wasmFixtureManifestBundle)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := BuildWasm(ctx, relDir, false)
	if err != nil {
		t.Fatalf("BuildWasm: %v", err)
	}
	if result == nil {
		t.Fatal("BuildWasm returned nil result for a module without wasm: false")
	}
	if _, err := os.Stat(result.WasmPath); err != nil {
		t.Errorf("wasm output not found at %s: %v", result.WasmPath, err)
	}
}

func TestBuildWasmNoopWhenWasmFalse(t *testing.T) {
	dir := t.TempDir()
	writeWasmFixture(t, dir, wasmFixtureManifestNoWasm)

	result, err := BuildWasm(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("BuildWasm: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for a module with wasm: false, got %+v", result)
	}
}
