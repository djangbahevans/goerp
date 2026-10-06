package module

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// declarationsExport is the wasm export the collection overlay adds to the
// module's main package; it writes sdk/go/declare's registry to stdout.
const declarationsExport = "goerp_declarations"

// declarationsOverlaySrc is added to the module's cmd/module package by a go
// build overlay, so a module needs no export of its own for collection.
const declarationsOverlaySrc = `package main

import (
	"os"

	"github.com/djangbahevans/goerp/sdk/go/declare"
)

//go:wasmexport ` + declarationsExport + `
func goerpDeclarations() {
	if err := declare.WriteTo(os.Stdout); err != nil {
		panic(err)
	}
}
`

// declarationsRunTimeout bounds how long the module's init() functions may
// run during collection.
const declarationsRunTimeout = time.Minute

// collectDeclarations builds dir's cmd/module package into a throwaway
// wasip1 reactor, runs its init() functions in a sandbox that provides no host
// functions, and reads back what they recorded into sdk/go/declare. plannedFiles
// maps absolute paths to the content the build sees there instead of what is
// on disk, with a nil content hiding the file, so the module compiles against
// the models Generate is about to write without writing them first.
func collectDeclarations(ctx context.Context, dir string, plannedFiles map[string][]byte) (Declarations, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve module directory: %w", err)
	}
	// Overlay keys match the symlink-resolved paths the go command uses for
	// the module's files.
	cmdModuleDir := filepath.Join(resolveSymlinksOrSelf(absDir), "cmd", "module")
	if info, err := os.Stat(cmdModuleDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("collect declarations: %s does not exist", cmdModuleDir)
	}

	scratchDir, err := os.MkdirTemp("", "goerp-declarations-*")
	if err != nil {
		return nil, fmt.Errorf("create declarations scratch directory: %w", err)
	}
	defer os.RemoveAll(scratchDir)

	replace := map[string]string{}
	overlaySrc := filepath.Join(scratchDir, "declarations.go")
	if err := os.WriteFile(overlaySrc, []byte(declarationsOverlaySrc), 0o644); err != nil {
		return nil, fmt.Errorf("write declarations source: %w", err)
	}
	replace[filepath.Join(cmdModuleDir, "zz_goerp_declarations.go")] = overlaySrc

	for i, path := range slices.Sorted(maps.Keys(plannedFiles)) {
		if plannedFiles[path] == nil {
			replace[path] = ""
			continue
		}
		planned := filepath.Join(scratchDir, fmt.Sprintf("planned-%d.go", i))
		if err := os.WriteFile(planned, plannedFiles[path], 0o644); err != nil {
			return nil, fmt.Errorf("write planned %s: %w", filepath.Base(path), err)
		}
		replace[path] = planned
	}

	overlay, err := json.Marshal(map[string]map[string]string{"Replace": replace})
	if err != nil {
		return nil, fmt.Errorf("encode build overlay: %w", err)
	}
	overlayPath := filepath.Join(scratchDir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
		return nil, fmt.Errorf("write build overlay: %w", err)
	}

	wasmPath := filepath.Join(scratchDir, "module.wasm")
	if err := runCmd(ctx, dir, []string{"GOOS=wasip1", "GOARCH=wasm"}, "go", "build",
		"-buildmode=c-shared", "-overlay", overlayPath, "-o", wasmPath, "./cmd/module"); err != nil {
		return nil, err
	}

	binary, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("read declarations binary: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, declarationsRunTimeout)
	defer cancel()
	stdout, err := runDeclarationsExport(runCtx, binary)
	if err != nil {
		return nil, err
	}

	var decls Declarations
	if err := json.Unmarshal(stdout, &decls); err != nil {
		return nil, fmt.Errorf("decode declarations: %w", err)
	}
	return decls, nil
}

// runDeclarationsExport instantiates binary as a WASI reactor under a fresh
// wazero runtime and calls its declarations export. WASI is available for
// stdout and stderr; every other import resolves to a function that fails the
// run, so a declaration that reaches for a host function fails collection
// instead of running with a fake one.
func runDeclarationsExport(ctx context.Context, binary []byte) ([]byte, error) {
	// The run is a few init() functions, so the interpreter's lack of a compile
	// step beats compiling the whole SDK-linked binary.
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigInterpreter().WithCloseOnContextDone(true))
	defer rt.Close(ctx)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return nil, fmt.Errorf("instantiate wasi: %w", err)
	}

	compiled, err := rt.CompileModule(ctx, binary)
	if err != nil {
		return nil, fmt.Errorf("compile module: %w", err)
	}
	if err := instantiateUnavailableHostModules(ctx, rt, compiled); err != nil {
		return nil, err
	}

	var stdout, stderr bytes.Buffer
	cfg := wazero.NewModuleConfig().WithStdout(&stdout).WithStderr(&stderr).WithStartFunctions("_initialize")

	mod, err := rt.InstantiateModule(ctx, compiled, cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize module: %w: %s", err, stderr.String())
	}

	// Whatever init() printed is not part of the declarations.
	stdout.Reset()

	export := mod.ExportedFunction(declarationsExport)
	if export == nil {
		return nil, fmt.Errorf("module does not export %s", declarationsExport)
	}
	if _, err := export.Call(ctx); err != nil {
		return nil, fmt.Errorf("read declarations: %w: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// instantiateUnavailableHostModules registers, for every function compiled
// imports outside WASI, a host function of the same signature that panics.
func instantiateUnavailableHostModules(ctx context.Context, rt wazero.Runtime, compiled wazero.CompiledModule) error {
	byModule := make(map[string][]api.FunctionDefinition)
	for _, fn := range compiled.ImportedFunctions() {
		moduleName, _, _ := fn.Import()
		if moduleName == wasi_snapshot_preview1.ModuleName {
			continue
		}
		byModule[moduleName] = append(byModule[moduleName], fn)
	}

	for moduleName, fns := range byModule {
		builder := rt.NewHostModuleBuilder(moduleName)
		for _, fn := range fns {
			_, name, _ := fn.Import()
			unavailable := api.GoModuleFunc(func(context.Context, api.Module, []uint64) {
				panic(fmt.Sprintf("host function %s.%s is not available while collecting declarations", moduleName, name))
			})
			builder.NewFunctionBuilder().
				WithGoModuleFunction(unavailable, fn.ParamTypes(), fn.ResultTypes()).
				Export(name)
		}
		if _, err := builder.Instantiate(ctx); err != nil {
			return fmt.Errorf("instantiate unavailable %s host module: %w", moduleName, err)
		}
	}
	return nil
}
