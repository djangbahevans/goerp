package module

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WasmBuildResult describes a successful BuildWasm call.
type WasmBuildResult struct {
	WasmPath   string
	WasmSHA256 string
}

// BuildWasm compiles a module's cmd/module package to a wasip1 WASI
// reactor binary (if the module's manifest doesn't declare wasm: false)
// and returns its sha256. It returns (nil, nil) when the module declares
// no WASM binary, so callers can distinguish "nothing to build" from a
// build failure. When debug is true, the build keeps debug symbols and
// full paths instead of stripping them.
func BuildWasm(ctx context.Context, dir string, debug bool) (*WasmBuildResult, error) {
	manifestPath := filepath.Join(dir, "manifest.json")

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	// manifest-spec.md §2: wasm defaults to true when omitted.
	wasm, hasWasm := decoded["wasm"].(bool)
	if hasWasm && !wasm {
		return nil, nil
	}

	cmdModuleDir := filepath.Join(dir, "cmd", "module")
	if info, err := os.Stat(cmdModuleDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("wasm build is enabled but %s does not exist", cmdModuleDir)
	}

	wasmPath := filepath.Join(dir, "module.wasm")

	// -o is relative to dir, since runCmd sets cmd.Dir to dir — a path
	// already joined with dir here would be resolved relative to dir
	// twice.
	args := []string{"build", "-buildmode=c-shared"}
	if !debug {
		args = append(args, "-trimpath", "-ldflags=-s -w")
	}
	args = append(args, "-o", "module.wasm", "./cmd/module")

	if err := runCmd(ctx, dir, []string{"GOOS=wasip1", "GOARCH=wasm"}, "go", args...); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("read wasm output: %w", err)
	}

	return &WasmBuildResult{
		WasmPath:   wasmPath,
		WasmSHA256: "sha256:" + computeSHA256(data),
	}, nil
}
