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

// SharedCompilationCacheDir returns one wazero compilation cache directory
// for the whole test process. A per-test directory recompiles the same
// fixture modules, which is ahead-of-time compilation of a multi-megabyte
// binary and dominates the run time of any package that loads them in many
// tests. The directory is not removed; it lives under the OS temp directory.
func SharedCompilationCacheDir() string { return compilationCacheDir() }
