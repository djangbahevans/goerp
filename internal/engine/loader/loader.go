// Package loader turns a module's manifest + WASM binary into a
// module.LoadedModule: manifest validation, checksum verification,
// compilation, capability resolution, pool creation, and the three
// no-argument export calls a module makes at load time
// (engine-internals.md §2, Stage 3 steps 15-17c-bis). Everything after
// that — schema sync, instance warming — belongs to later stages and
// other tickets; this package only produces the LoadedModule those
// stages consume. Synchronous-subscription cycle detection
// (engine-internals.md §2 Stage 3 step 23) lives in
// registry.ModuleRegistry.Update instead of here, since it needs the
// full cross-module event graph that package already assembles via
// buildEventRegistry, not just one batch of freshly-loaded sources.
// LoadModule merges EnableViews/Nav candidates into each module's own
// Manifest.Views/Navigation (route.SynthesizeViews) before returning it
// — validated against that same module's EnableOps, but not itself an
// EnableOps merge. LoadAll separately registers EnableOps-derived CRUD
// routes (route.RegisterModelRoutes) alongside each module's explicit
// routes across the whole batch, since that needs the shared route table
// LoadAll already builds.
package loader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/job"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

// Source is one module's manifest and WASM binary, already read from disk
// (or object storage) by the caller — discovering module sources and
// determining load order is out of this package's scope.
type Source struct {
	Name          string
	ManifestBytes []byte
	WasmBytes     []byte
	// PackagePath is the .erp package file or loose module directory src
	// was read from on disk. Copied onto the returned LoadedModule
	// unchanged — LoadModule itself never reads it.
	PackagePath string
}

// LoadModule validates src's manifest, verifies its WASM binary's
// checksum, compiles it (or loads it from the compilation cache), resolves
// its declared capabilities, creates its instance pool, and calls
// get_routes/get_model_declarations/get_data_migrations on a temporary
// instance, caching each result on the returned LoadedModule. It also
// merges each model's EnableViews/Nav candidates into the module's own
// Manifest.Views/Navigation (route.SynthesizeViews) — a failure there
// fails the load the same as any other step. On any failure it returns a
// LoadedModule with Status StatusFailed and FailureReason set, rather
// than an error — every module in a load batch gets a LoadedModule,
// successful or not (module.LoadedModule doc comment).
//
// LoadModule never touches a shared route table itself — a single module
// has no visibility into what other modules have already claimed. See
// LoadAll for the multi-module loop that does.
func LoadModule(ctx context.Context, rt *wasm.Runtime, poolCfg wasm.PoolConfig, src Source) *module.LoadedModule {
	m := &module.LoadedModule{Status: module.StatusCompiling, PackagePath: src.PackagePath}

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

	if err := validateModuleRoutes(mf, routes); err != nil {
		m.Fail(err.Error())
		return m
	}

	models, types, err := callGetModelDeclarations(ctx, tempInst)
	if err != nil {
		m.Fail(fmt.Sprintf("get_model_declarations: %v", err))
		return m
	}
	m.ModelDecls = models
	m.TypeDecls = types

	if err := validateVirtualModels(ctx, tempInst, mf, models); err != nil {
		m.Fail(err.Error())
		return m
	}

	if err := validateTransientModels(models); err != nil {
		m.Fail(err.Error())
		return m
	}

	synthesizedViews, suppressedViews, nav, err := route.SynthesizeViews(src.Name, mf.Type, models, mf.Views, mf.Navigation)
	if err != nil {
		m.Fail(fmt.Sprintf("synthesize views: %v", err))
		return m
	}
	for _, s := range suppressedViews {
		log.Warn().Str("module", src.Name).Str("model", s.Model).Str("view", s.View).
			Msg("EnableViews: hand-declared view already registered, auto-derived view suppressed")
	}
	// Views is appended to (synthesizedViews excludes anything suppressed by
	// a collision); nav is already the merged tree SynthesizeViews returns,
	// so Navigation is replaced outright rather than appended.
	m.Manifest.Views = append(m.Manifest.Views, synthesizedViews...)
	m.Manifest.Navigation = nav

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

// LoadAll loads every source in order, registering each module's routes and
// job types against one running table/registry as it's loaded — via
// route.RegisterModuleRoutes and job.JobRegistry.Register directly, not
// registry.ModuleRegistry.Update, whose build* functions each rebuild from
// a full map in one pass and abort the entire batch on the first conflict
// they find. Registering incrementally here means a module that fails its
// own route or job-type registration (a reserved route namespace, a
// cross-module job type name collision) is marked StatusFailed on its own,
// without invalidating any module already registered before it. The
// table/registry built here is discarded once LoadAll returns — only the
// StatusFailed markings they produced on the returned modules map persist;
// registry.ModuleRegistry.Update rebuilds the real ones downstream from
// that map.
//
// Determining dependency order is the caller's responsibility, same as
// Source discovery — sources are loaded in the order given.
func LoadAll(ctx context.Context, rt *wasm.Runtime, poolCfg wasm.PoolConfig, sources []Source) map[string]*module.LoadedModule {
	modules := make(map[string]*module.LoadedModule, len(sources))
	table := route.New()
	jobs := job.New()

	for _, src := range sources {
		m := LoadModule(ctx, rt, poolCfg, src)
		if m.Status != module.StatusFailed {
			explicit := route.ExplicitRoutesFrom(m.ExplicitRoutes)
			if suppressed, err := route.RegisterRoutes(table, src.Name, m.Manifest.Type, explicit, m.ModelDecls); err != nil {
				m.Fail(err.Error())
			} else {
				for _, s := range suppressed {
					log.Warn().Str("module", src.Name).Str("model", s.Model).Str("op", s.Op).
						Msg("EnableOps: explicit route already registered, auto-derived route suppressed")
				}
			}
		}
		if m.Status != module.StatusFailed {
			if err := jobs.Register(src.Name, m.Manifest.JobTypes); err != nil {
				m.Fail(err.Error())
			}
		}
		modules[src.Name] = m
	}

	ValidateEventSubscriptions(modules)

	return modules
}

// ValidateEventSubscriptions checks every loaded module's subscribes[].name
// against the set of events actually emitted by loaded modules (the event
// registry goerp#68 builds). A subscribes[].name that names no known
// event fails the subscribing module's load — unless the event's owning
// module (the {module} segment of its {module}.{noun}.{verb} name) is
// declared in the subscriber's soft_depends_on, in which case it's a
// load-time warning and the module still loads: a soft dependency is
// allowed to be absent, so its events being unknown is expected, not an
// error.
//
// Exported so a caller loading modules one at a time (not via LoadAll) can
// still run this same validation once its own loop finishes.
func ValidateEventSubscriptions(modules map[string]*module.LoadedModule) {
	knownEmits := make(map[string]bool)
	for _, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		for _, emit := range m.Manifest.Emits {
			knownEmits[emit.Name] = true
		}
	}

	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}

		for _, sub := range m.Manifest.Subscribes {
			if knownEmits[sub.Name] {
				continue
			}

			owner, _, _ := strings.Cut(sub.Name, ".")
			if slices.Contains(m.Manifest.SoftDependsOn, owner) {
				log.Warn().
					Str("module", name).
					Str("event", sub.Name).
					Msg("subscribes to an event no loaded module emits; owning module is a soft dependency")
				continue
			}

			m.Fail(fmt.Sprintf("subscribes to unknown event %q", sub.Name))
			break
		}
	}
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
// actual wire format, model.Schema{Types, Models} (go-sdk-reference.md),
// and returns its Models half as value types — LoadedModule.ModelDecls is
// []model.ModelDeclaration, not the []*ModelDeclaration model.Schema
// itself carries. Types round-trips as-is.
func callGetModelDeclarations(ctx context.Context, inst *wasm.ModuleInstance) ([]model.ModelDeclaration, []model.TypeDeclaration, error) {
	data, err := inst.InvokeNoArg(ctx, "get_model_declarations")
	if err != nil {
		return nil, nil, err
	}
	var schema model.Schema
	if err := msgpack.Unmarshal(data, &schema); err != nil {
		return nil, nil, fmt.Errorf("unmarshal get_model_declarations response: %w", err)
	}
	decls := make([]model.ModelDeclaration, 0, len(schema.Models))
	for _, d := range schema.Models {
		if d != nil {
			decls = append(decls, *d)
		}
	}
	return decls, schema.Types, nil
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

// validateModuleRoutes enforces manifest-spec.md §3's "may register
// routes" column: of the 8 module types, only domain and connector allow
// registering routes — l10n, bridge, theme, report_bundle, and
// automation all forbid it. Detecting a violation needs the actual
// get_routes() result, not a manifest field alone (there's no manifest
// routes key to check), which is why this lives in the loader package
// rather than alongside manifest.validateModuleType's other per-type
// checks.
func validateModuleRoutes(mf *manifest.Manifest, routes []engine.RouteDeclaration) error {
	switch mf.Type {
	case "l10n", "bridge", "theme", "report_bundle", "automation":
		if len(routes) > 0 {
			return fmt.Errorf("type %q must not register routes, got %d", mf.Type, len(routes))
		}
	}
	return nil
}

// validateVirtualModels enforces the two Virtual-model load-time rules
// go-sdk-reference.md §22 documents: a Virtual model may only be declared
// in a type: connector module, and (once that holds) EnableOps(Create)
// requires a registered Create backend function while EnableOps(List)
// rejects any declared ABAC condition — row-filtered access to a Virtual
// model is Get-by-ID only, since ABAC filtering happens after the
// backend has already fetched and paginated against the external
// source. get_virtual_backends is only called when at least one model
// actually declares Virtual — InvokeNoArg errors hard on a missing
// export, so calling it unconditionally would require every module,
// Virtual or not, to implement it.
func validateVirtualModels(ctx context.Context, inst *wasm.ModuleInstance, mf *manifest.Manifest, models []model.ModelDeclaration) error {
	hasVirtual := false
	for _, md := range models {
		if md.Backend == model.BackendVirtual {
			hasVirtual = true
			break
		}
	}
	if !hasVirtual {
		return nil
	}
	if mf.Type != "connector" {
		return fmt.Errorf("model.Virtual() is only permitted in modules of type: connector")
	}

	backends, err := callGetVirtualBackends(ctx, inst)
	if err != nil {
		return fmt.Errorf("get_virtual_backends: %w", err)
	}

	for _, md := range models {
		if md.Backend != model.BackendVirtual {
			continue
		}
		registeredOps := backends[md.Name]
		for _, op := range md.EnabledOps {
			if op.Name == "list" && op.Condition != "" {
				return fmt.Errorf("model %s: EnableOps(List) with an ABAC condition is not allowed on a Virtual model", md.Name)
			}
			if op.Name == "create" && !slices.Contains(registeredOps, "create") {
				return fmt.Errorf("model %s: EnableOps(Create) declared with no registered Create backend function", md.Name)
			}
		}
	}
	return nil
}

// validateTransientModels enforces the two Transient-model load-time
// rules go-sdk-reference.md §22 documents: EnableOps(List) is rejected
// outright (a Transient model has no browse semantics — it's addressed
// directly by the ID create returns), and a declared TTL must be
// positive (a zero or negative TTL would mean every SET immediately
// expires, or Redis rejecting the EXPIRE outright, either way a
// model that can never actually hold state). Unlike Virtual, Transient
// carries no connector-only restriction — any module type may declare
// one.
func validateTransientModels(models []model.ModelDeclaration) error {
	for _, md := range models {
		if md.Backend != model.BackendTransient {
			continue
		}
		if md.TransientTTLSeconds <= 0 {
			return fmt.Errorf("model %s: Transient() requires a positive TTL", md.Name)
		}
		for _, op := range md.EnabledOps {
			if op.Name == "list" {
				return fmt.Errorf("model %s: EnableOps(List) is not allowed on a Transient model", md.Name)
			}
		}
	}
	return nil
}

func callGetVirtualBackends(ctx context.Context, inst *wasm.ModuleInstance) (map[string][]string, error) {
	data, err := inst.InvokeNoArg(ctx, "get_virtual_backends")
	if err != nil {
		return nil, err
	}
	var backends map[string][]string
	if err := msgpack.Unmarshal(data, &backends); err != nil {
		return nil, fmt.Errorf("unmarshal get_virtual_backends response: %w", err)
	}
	return backends, nil
}
