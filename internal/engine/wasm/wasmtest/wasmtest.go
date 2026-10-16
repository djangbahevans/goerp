// Package wasmtest holds helpers shared by the engine's WASM-backed tests.
package wasmtest

import (
	"bytes"
	"os"
	"os/exec"
	"sync"
	"testing"
)

var compilationCacheDir = sync.OnceValue(func() string {
	dir, err := os.MkdirTemp("", "goerp-wasm-test-cache-")
	if err != nil {
		panic(err)
	}
	return dir
})

// SharedCompilationCacheDir uses GOERP_TEST_WASM_CACHE to reuse compiled fixtures
// across test processes. Otherwise, it returns a temporary directory per process.
func SharedCompilationCacheDir() string {
	if dir := os.Getenv("GOERP_TEST_WASM_CACHE"); dir != "" {
		return dir
	}

	return compilationCacheDir()
}

// Precompile prepares native code in cacheDir using GOERP_TEST_WASM_PRECOMPILER.
// It does nothing when the executable is unset.
func Precompile(t testing.TB, binary []byte, cacheDir string) {
	t.Helper()

	executable := os.Getenv("GOERP_TEST_WASM_PRECOMPILER")
	if executable == "" {
		return
	}

	cmd := exec.CommandContext(t.Context(), executable, "-cache", cacheDir)
	cmd.Stdin = bytes.NewReader(binary)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("precompile WASM fixture: %v\n%s", err, output)
	}
}
