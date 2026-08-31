package moduleboot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	internalmodule "github.com/djangbahevans/goerp/internal/module"
)

const e2eFixtureManifest = `{
  "abi_version": "1",
  "capabilities": [],
  "checksum": "sha256:empty",
  "depends_on": [],
  "description": "end-to-end .erp discovery fixture",
  "display_name": "E2E Fixture",
  "engine": ">=0.1.0",
  "name": "e2efixture",
  "schema": {
    "owned_models": []
  },
  "type": "domain",
  "version": "1.0.0"
}
`

const e2eFixtureGoMod = `module goerp-moduleboot-e2e-fixture

go 1.27.0
`

const e2eFixtureMainGo = `package main

func main() {}
`

// TestDiscover_EndToEndWithRealErpBuild is the goerp#13 acceptance test:
// build a real (trivial) module via internal/module.Package — the same
// implementation goerp module build itself calls — then load it through
// Discover and confirm the parsed manifest and wasm bytes match. Frontend
// packaging is skipped (SkipFrontend) since it's orthogonal to what this
// ticket touches (manifest/wasm extraction from a real .erp), and skipping
// it avoids an npm dependency in this test.
func TestDiscover_EndToEndWithRealErpBuild(t *testing.T) {
	moduleDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(moduleDir, "manifest.json"), []byte(e2eFixtureManifest), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(e2eFixtureGoMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	cmdModuleDir := filepath.Join(moduleDir, "cmd", "module")
	if err := os.MkdirAll(cmdModuleDir, 0o755); err != nil {
		t.Fatalf("mkdir cmd/module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cmdModuleDir, "main.go"), []byte(e2eFixtureMainGo), 0o644); err != nil {
		t.Fatalf("write cmd/module/main.go: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if _, err := internalmodule.Package(ctx, moduleDir, internalmodule.PackageOptions{SkipFrontend: true}); err != nil {
		t.Fatalf("Package: %v", err)
	}

	// Package's default output lands at <moduleDir>/build/<name>-<version>.erp
	// — Discover that directory directly, matching how goerp module build's
	// real output layout is meant to be scanned.
	sources, err := Discover(filepath.Join(moduleDir, "build"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("Discover() returned %d sources, want 1", len(sources))
	}

	src := sources[0]
	if src.Name != "e2efixture" {
		t.Errorf("Name = %q, want %q", src.Name, "e2efixture")
	}

	mf, err := manifest.Load(src.ManifestBytes)
	if err != nil {
		t.Fatalf("manifest.Load(src.ManifestBytes): %v", err)
	}
	if mf.Name != "e2efixture" || mf.Version != "1.0.0" {
		t.Errorf("parsed manifest Name/Version = %q/%q, want %q/%q", mf.Name, mf.Version, "e2efixture", "1.0.0")
	}

	wantWasm, err := os.ReadFile(filepath.Join(moduleDir, "module.wasm"))
	if err != nil {
		t.Fatalf("read module.wasm built by Package: %v", err)
	}
	if len(src.WasmBytes) == 0 {
		t.Fatal("Discover produced empty WasmBytes")
	}
	if string(src.WasmBytes) != string(wantWasm) {
		t.Error("Discover's WasmBytes don't match the module.wasm Package actually built")
	}

	sum := sha256.Sum256(src.WasmBytes)
	if mf.Checksum != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Errorf("manifest checksum %q doesn't match the extracted wasm's own sha256", mf.Checksum)
	}
}
