// Package loader turns a module's manifest + WASM binary into a
// module.LoadedModule: manifest validation, checksum verification,
// compilation, capability resolution, pool creation, and the three
// no-argument export calls a module makes at load time
// (engine-internals.md §2, Stage 3 steps 15-17c-bis). Everything after
// that — schema sync, instance warming, cross-module event-cycle
// detection, EnableOps-derived route/view merging — belongs to later
// stages and other tickets; this package only produces the
// LoadedModule those stages consume.
package loader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/vmihailenco/msgpack/v5"
)

// Source is one module's manifest and WASM binary, already read from disk
// (or object storage) by the caller — discovering module sources and
// determining load order is out of this package's scope.
type Source struct {
	Name          string
	ManifestBytes []byte
	WasmBytes     []byte
}

// LoadModule validates src's manifest, verifies its WASM binary's
// checksum, compiles it (or loads it from the compilation cache), resolves
// its declared capabilities, creates its instance pool, and calls
// get_routes/get_model_declarations/get_data_migrations on a temporary
// instance, caching each result on the returned LoadedModule. On any
// failure it returns a LoadedModule with Status StatusFailed and
// FailureReason set, rather than an error — every module in a load batch
// gets a LoadedModule, successful or not (module.LoadedModule doc comment).
//
// LoadModule never touches a shared route table itself — a single module
// has no visibility into what other modules have already claimed. See
// LoadAll for the multi-module loop that does.
func LoadModule(ctx context.Context, rt *wasm.Runtime, poolCfg wasm.PoolConfig, src Source) *module.LoadedModule {
	m := &module.LoadedModule{Status: module.StatusCompiling}

	mf, err := manifest.Load(src.ManifestBytes)
	if err != nil {
		m.Fail(fmt.Sprintf("invalid manifest: %v", err))
		return m
	}
	m.Manifest = *mf

	if err := verifyChecksum(mf.Checksum, src.WasmBytes); err != nil {
		m.Fail(err.Error())
		return m
	}

	compiled, err := rt.CompileModule(ctx, src.WasmBytes)
	if err != nil {
		m.Fail(fmt.Sprintf("compile: %v", err))
		return m
	}
	m.CompiledModule = compiled

	caps, err := abi.ResolveCapabilities(mf.Capabilities)
	if err != nil {
		m.Fail(fmt.Sprintf("resolve capabilities: %v", err))
		return m
	}
	m.Capabilities = caps

	m.Pool = rt.NewPool(src.Name, compiled, poolCfg)
	// NewPool starts replenishLoop immediately, which begins warming live
	// wasm instances in the background — every step below can still fail
	// the load, and nothing else in the engine ever closes a StatusFailed
	// module's pool or compiled module. Without this, a late failure here
	// (or a route conflict caught by LoadAll after this function returns)
	// leaks the replenish goroutine and its warmed instances for the
	// lifetime of the process.
	defer func() {
		if m.Status == module.StatusFailed {
			m.Pool.DrainAndClose(context.Background(), 5*time.Second)
			_ = compiled.Close(context.Background())
		}
	}()

	tempInst, err := rt.InstantiateTemp(ctx, src.Name, compiled)
	if err != nil {
		m.Fail(fmt.Sprintf("temp instantiation: %v", err))
		return m
	}
	defer func() { _ = tempInst.Module().Close(ctx) }()

	routes, err := callGetRoutes(ctx, tempInst)
	if err != nil {
		m.Fail(fmt.Sprintf("get_routes: %v", err))
		return m
	}
	m.ExplicitRoutes = routes

	models, err := callGetModelDeclarations(ctx, tempInst)
	if err != nil {
		m.Fail(fmt.Sprintf("get_model_declarations: %v", err))
		return m
	}
	m.ModelDecls = models

	migrations, err := callGetDataMigrations(ctx, tempInst)
	if err != nil {
		m.Fail(fmt.Sprintf("get_data_migrations: %v", err))
		return m
	}
	m.DataMigrations = migrations

	// StatusSyncing, not StatusReady: this function's scope ends at Stage 3
	// (engine-internals.md §2, steps 15-17c-bis) — schema sync (Stage 4) and
	// instance warming (Stage 5) haven't run yet, and nothing in this
	// package can run them. Claiming StatusReady here would tell a reader
	// "safe to route traffic to" before that's true. Whichever caller wires
	// Stage 4/5 in next (goerp#29's module install, or a future bulk-load
	// path) is responsible for advancing Status the rest of the way once it
	// actually runs those stages.
	m.Status = module.StatusSyncing
	m.LoadedAt = time.Now()
	return m
}

// LoadAll loads every source in order, registering each module's routes
// against one running table as it's loaded — via route.RegisterModuleRoutes
// directly, not registry.ModuleRegistry.Update, whose buildRouteTable
// rebuilds every module's routes from a full map in one pass and aborts
// the entire batch on the first conflict it finds. Registering
// incrementally here means a module that fails its own route registration
// (a reserved namespace, a self-duplicate route) is marked StatusFailed on
// its own, without invalidating any module already registered before it.
//
// Determining dependency order is the caller's responsibility, same as
// Source discovery — sources are loaded in the order given.
func LoadAll(ctx context.Context, rt *wasm.Runtime, poolCfg wasm.PoolConfig, sources []Source) map[string]*module.LoadedModule {
	modules := make(map[string]*module.LoadedModule, len(sources))
	table := route.New()

	for _, src := range sources {
		m := LoadModule(ctx, rt, poolCfg, src)
		if m.Status != module.StatusFailed {
			explicit := make([]route.ExplicitRoute, len(m.ExplicitRoutes))
			for i, r := range m.ExplicitRoutes {
				explicit[i] = route.ExplicitRoute{Method: r.Method, Path: r.Path}
			}
			if err := route.RegisterModuleRoutes(table, src.Name, m.Manifest.Type, explicit); err != nil {
				m.Fail(err.Error())
			}
		}
		modules[src.Name] = m
	}

	return modules
}

// verifyChecksum compares checksum (manifest-spec.md §2's
// "sha256:<hex>"-prefixed format) against the actual SHA-256 of wasmBytes.
func verifyChecksum(checksum string, wasmBytes []byte) error {
	hexPart, ok := strings.CutPrefix(checksum, "sha256:")
	if !ok {
		return fmt.Errorf("checksum %q missing sha256: prefix", checksum)
	}
	want, err := hex.DecodeString(hexPart)
	if err != nil {
		return fmt.Errorf("checksum %q is not valid hex: %w", checksum, err)
	}
	got := sha256.Sum256(wasmBytes)
	if !bytes.Equal(got[:], want) {
		return fmt.Errorf("checksum mismatch: manifest declares %s, binary hashes to sha256:%x", checksum, got)
	}
	return nil
}

func callGetRoutes(ctx context.Context, inst *wasm.ModuleInstance) ([]engine.RouteDeclaration, error) {
	data, err := inst.InvokeNoArg(ctx, "get_routes")
	if err != nil {
		return nil, err
	}
	var routes []engine.RouteDeclaration
	if err := msgpack.Unmarshal(data, &routes); err != nil {
		return nil, fmt.Errorf("unmarshal get_routes response: %w", err)
	}
	return routes, nil
}

// callGetModelDeclarations deserializes the get_model_declarations export's
// actual wire format, model.Schema{Models} (go-sdk-reference.md), and
// returns its Models half as value types — LoadedModule.ModelDecls is
// []model.ModelDeclaration, not the []*ModelDeclaration model.Schema
// itself carries.
func callGetModelDeclarations(ctx context.Context, inst *wasm.ModuleInstance) ([]model.ModelDeclaration, error) {
	data, err := inst.InvokeNoArg(ctx, "get_model_declarations")
	if err != nil {
		return nil, err
	}
	var schema model.Schema
	if err := msgpack.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("unmarshal get_model_declarations response: %w", err)
	}
	decls := make([]model.ModelDeclaration, 0, len(schema.Models))
	for _, d := range schema.Models {
		if d != nil {
			decls = append(decls, *d)
		}
	}
	return decls, nil
}

func callGetDataMigrations(ctx context.Context, inst *wasm.ModuleInstance) ([]model.DataMigration, error) {
	data, err := inst.InvokeNoArg(ctx, "get_data_migrations")
	if err != nil {
		return nil, err
	}
	var migrations []model.DataMigration
	if err := msgpack.Unmarshal(data, &migrations); err != nil {
		return nil, fmt.Errorf("unmarshal get_data_migrations response: %w", err)
	}
	return migrations, nil
}
