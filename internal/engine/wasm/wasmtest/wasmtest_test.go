package wasmtest

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/tetratelabs/wazero"
)

func TestCompilationCacheAcrossProcesses(t *testing.T) {
	if value := os.Getenv("GOERP_WASM_CACHE_CHILD"); value != "" {
		want, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}

		compileCachedModule(t, byte(want))
		return
	}

	dir := t.TempDir()
	t.Setenv("GOERP_TEST_WASM_CACHE", dir)
	var cached map[string]fs.FileInfo
	for i, value := range []string{"42", "42", "43"} {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCompilationCacheAcrossProcesses$")
		cmd.Env = append(os.Environ(), "GOERP_WASM_CACHE_CHILD="+value)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run cache consumer: %v\n%s", err, output)
		}

		entries := cacheEntries(t, dir)
		if len(entries) == 0 {
			t.Fatal("compiled module was not persisted")
		}
		if i == 1 {
			if len(entries) != len(cached) {
				t.Fatal("identical module added cache entries")
			}
			for path, before := range cached {
				after, ok := entries[path]
				if !ok || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
					t.Fatalf("cached compilation was replaced: %s", path)
				}
			}
		}
		if i == 2 && len(entries) <= len(cached) {
			t.Fatal("changed module did not produce a new cache entry")
		}
		cached = entries
	}
}

func compileCachedModule(t *testing.T, value byte) {
	t.Helper()

	cache, err := wazero.NewCompilationCacheWithDir(SharedCompilationCacheDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close(context.Background()) })

	rt := wazero.NewRuntimeWithConfig(t.Context(), wazero.NewRuntimeConfig().WithCompilationCache(cache))
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	binary := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
		0x03, 0x02, 0x01, 0x00,
		0x05, 0x03, 0x01, 0x00, 0x01,
		0x07, 0x13, 0x02, 0x06, 'a', 'n', 's', 'w', 'e', 'r', 0x00, 0x00,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, value, 0x0b,
	}
	mod, err := rt.Instantiate(t.Context(), binary)
	if err != nil {
		t.Fatal(err)
	}

	result, err := mod.ExportedFunction("answer").Call(t.Context())
	if err != nil || len(result) != 1 || result[0] != uint64(value) {
		t.Fatalf("answer = %v, error = %v, want %d", result, err, value)
	}
	if got, ok := mod.Memory().ReadByte(0); !ok || got != 0 {
		t.Fatalf("fresh instance memory = %d, readable = %t", got, ok)
	}
	if !mod.Memory().WriteByte(0, 99) {
		t.Fatal("write instance memory")
	}
}

func cacheEntries(t *testing.T, dir string) map[string]fs.FileInfo {
	t.Helper()

	entries := make(map[string]fs.FileInfo)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		entries[path] = info
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
