package codegen

import (
	"bytes"
	"context"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

// copyExampleModule copies testdata/examplemod into a temp directory and
// overlays a go.work onto this repo's checkout, so the module's
// sdk/go/engine and sdk/go/model imports resolve without a require.
func copyExampleModule(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	src := filepath.Join("testdata", "examplemod")
	dir := filepath.Join(t.TempDir(), "examplemod")
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy example module: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "work", "init", ".", repoRoot)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go work init: %v\n%s", err, out)
	}
	return dir
}

// generateExample builds the example module and generates its client.
func generateExample(t *testing.T, dir string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	in, err := LoadLocal(ctx, dir)
	if err != nil {
		t.Fatalf("LoadLocal() error: %v", err)
	}
	out, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	return out
}

func TestLoadLocal_ExampleModuleMatchesGolden(t *testing.T) {
	dir := copyExampleModule(t)

	first := generateExample(t, dir)
	wasmInfo, err := os.Stat(filepath.Join(dir, "module.wasm"))
	if err != nil {
		t.Fatalf("LoadLocal did not build module.wasm: %v", err)
	}

	golden := filepath.Join("testdata", "examplemod.generated.ts")
	if *update {
		if err := os.WriteFile(golden, first, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if !bytes.Equal(first, want) {
		t.Errorf("generated client differs from %s (run with -update to accept):\n%s", golden, first)
	}

	second := generateExample(t, dir)
	if !bytes.Equal(first, second) {
		t.Errorf("two consecutive runs differ:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if info, err := os.Stat(filepath.Join(dir, "module.wasm")); err != nil || !info.ModTime().Equal(wasmInfo.ModTime()) {
		t.Errorf("second run rebuilt an up-to-date module.wasm")
	}
}

func TestLoadLocal_RebuildsStaleWasm(t *testing.T) {
	dir := copyExampleModule(t)
	generateExample(t, dir)

	mainGo := filepath.Join(dir, "cmd", "module", "main.go")
	src, err := os.ReadFile(mainGo)
	if err != nil {
		t.Fatal(err)
	}
	src = bytes.Replace(src, []byte(`engine.GET("/export", ok)`), []byte(`engine.GET("/export", ok)
	engine.PUT("/settings", ok)`), 1)
	if err := os.WriteFile(mainGo, src, 0o644); err != nil {
		t.Fatal(err)
	}
	// A source newer than the binary, whatever the filesystem's timestamp
	// granularity.
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(mainGo, future, future); err != nil {
		t.Fatal(err)
	}

	if out := generateExample(t, dir); !bytes.Contains(out, []byte("putSettings")) {
		t.Errorf("after a source change, output has no putSettings: the stale module.wasm was not rebuilt")
	}
}
