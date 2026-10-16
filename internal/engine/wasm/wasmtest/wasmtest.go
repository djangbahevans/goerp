// Package wasmtest holds helpers shared by the engine's WASM-backed tests.
package wasmtest

import (
	"os"
	"sync"
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
