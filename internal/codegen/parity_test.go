package codegen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
)

// TestFromEngine_MatchesLocal loads the example module through the
// engine's own loader and checks that generating from its /_meta/schema
// gives the same file a local run does.
func TestFromEngine_MatchesLocal(t *testing.T) {
	dir := copyExampleModule(t)
	local := generateExample(t, dir)

	wasmBytes, err := os.ReadFile(filepath.Join(dir, "module.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	var mf map[string]any
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &mf); err != nil {
		t.Fatal(err)
	}
	mf["checksum"] = fmt.Sprintf("sha256:%x", sha256.Sum256(wasmBytes))
	manifestBytes, err := json.Marshal(mf)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	rt, err := wasm.New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	m := loader.LoadModule(ctx, rt, wasm.PoolConfig{MaxSize: 1, BorrowTimeout: time.Second}, loader.Source{
		Name: "contacts", ManifestBytes: manifestBytes, WasmBytes: wasmBytes,
	})
	if m.Status == module.StatusFailed {
		t.Fatalf("LoadModule failed: %s", m.FailureReason)
	}
	t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })

	snap, err := (&registry.ModuleRegistry{}).Update(map[string]*module.LoadedModule{"contacts": m})
	if err != nil {
		t.Fatalf("registry Update: %v", err)
	}
	schema, err := json.Marshal(snap.SchemaResponse())
	if err != nil {
		t.Fatal(err)
	}
	in, err := InputFromSchema(schema, "contacts")
	if err != nil {
		t.Fatalf("InputFromSchema: %v", err)
	}
	fromEngine, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if !bytes.Equal(local, fromEngine) {
		t.Errorf("--from-engine output differs from --local:\n--local:\n%s\n--from-engine:\n%s", local, fromEngine)
	}
}
