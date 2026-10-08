// Package loader validates and compiles module packages and resolves their declarations.
// Schema sync and pool warming consume its results; the registry checks cross-module
// subscription cycles.
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

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/dbscope"
	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/internal/engine/job"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/orm"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

// Source contains module bytes read by the caller; discovery and dependency ordering
// happen before loading.
type Source struct {
	Name          string
	ManifestBytes []byte
	WasmBytes     []byte
	// BundleBytes is the frontend bundle's raw bytes (frontend/dist/
	// bundle.<hash>.js in the .erp archive layout), nil when the module's
	// package carries none. Verified against manifest.Frontend.BundleSHA256
	// by LoadModule the same way WasmBytes is verified against Checksum —
	// present regardless of whether the manifest actually declares
	// frontend.bundle: true, so LoadModule can tell "declared but missing"
	// (a load failure) apart from "not declared" (nothing to verify).
	BundleBytes []byte
	// FrontendTranslations is the package's frontend/translations/*.json
	// files keyed by locale (the file name without .json), nil when it has
	// none. LoadModule validates each one.
	FrontendTranslations map[string][]byte
	// PackagePath is the .erp package file or loose module directory src
	// was read from on disk. Copied onto the returned LoadedModule
	// unchanged — LoadModule itself never reads it.
	PackagePath string
}

// LoadModule validates and compiles a source, creates its pool and resolves declarations.
// Failures return a StatusFailed module with a reason; cross-module route registration
// belongs to the batch loader.
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

	if err := verifyBundle(mf, src.BundleBytes); err != nil {
		m.Fail(err.Error())
		return m
	}

	for locale, data := range src.FrontendTranslations {
		if err := l10n.ValidateFrontendTranslation(locale, data); err != nil {
			m.Fail(err.Error())
			return m
		}
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
	// Pool warming starts immediately. A subsequent load failure must close its goroutine
	// and instances because failed modules are not retained for shutdown cleanup.
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

	if err := validateWebhookVerifier(mf, tempInst); err != nil {
		m.Fail(err.Error())
		return m
	}
	m.HasWebhookVerifier = tempInst.HasWebhookVerifier()

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

	if err := validateWorkflowTransitions(models); err != nil {
		m.Fail(err.Error())
		return m
	}

	if err := validateTrackedFields(models); err != nil {
		m.Fail(err.Error())
		return m
	}

	if err := validateLifecycleEvents(models); err != nil {
		m.Fail(err.Error())
		return m
	}

	if err := validateReservedTableNames(models); err != nil {
		m.Fail(err.Error())
		return m
	}

	if err := validateSequenceFormats(models); err != nil {
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

	// Schema sync and pool warming must complete before the caller advances this module to
	// StatusReady.
	m.Status = module.StatusSyncing
	m.LoadedAt = time.Now()
	return m
}

// LoadAll loads sources in caller-supplied order and checks routes/job types
// incrementally. Conflicts fail only the later module, preserving earlier successful
// loads.
func LoadAll(ctx context.Context, rt *wasm.Runtime, poolCfg wasm.PoolConfig, sources []Source) map[string]*module.LoadedModule {
	modules := make(map[string]*module.LoadedModule, len(sources))
	table := route.New()
	jobs := job.New()
	permOwners := make(map[string]string) // permission name -> declaring module

	for _, src := range sources {
		m := LoadModule(ctx, rt, poolCfg, src)
		if m.Status != module.StatusFailed {
			explicit := route.ExplicitRoutesFrom(m.ExplicitRoutes)
			if suppressed, err := route.RegisterRoutes(table, src.Name, m.Manifest.Type, explicit, m.ModelDecls); err != nil {
				m.Fail(err.Error())
			} else {
				for _, s := range suppressed {
					log.Warn().Str("module", src.Name).Str("model", s.Model).Str("op", s.Op).
						Msg(s.LogMessage())
				}
			}
		}
		if m.Status != module.StatusFailed {
			if err := jobs.Register(src.Name, m.Manifest.JobTypes); err != nil {
				m.Fail(err.Error())
			}
		}
		if m.Status != module.StatusFailed {
			if owner, name, ok := FindPermissionCollision(permOwners, m.Manifest.Permissions); ok {
				m.Fail(fmt.Sprintf("permission %q already declared by module %q", name, owner))
			} else {
				for _, p := range m.Manifest.Permissions {
					permOwners[p.Name] = src.Name
				}
			}
		}
		modules[src.Name] = m
	}

	ValidateEventSubscriptions(modules)
	ValidateUsesConfig(modules)
	ValidateUsesPermissions(modules)
	LogViewExtensionConflicts(ValidateViewExtensions(modules))

	return modules
}

// FindPermissionCollision rejects a later module claiming another module's permission
// name. Registry rebuilds are idempotent and cannot enforce this load-order constraint.
func FindPermissionCollision(owners map[string]string, candidate []manifest.Permission) (owner, name string, ok bool) {
	for _, p := range candidate {
		if o, exists := owners[p.Name]; exists {
			return o, p.Name, true
		}
	}
	return "", "", false
}

// ValidateEventSubscriptions matches emitted events by exact name and version. Missing
// events fail the subscriber's load unless their owner is a soft dependency, in which case
// validation warns.
func ValidateEventSubscriptions(modules map[string]*module.LoadedModule) {
	emits := make(map[string][]manifest.EventDeclaration)
	for _, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		for _, emit := range m.Manifest.Emits {
			emits[emit.Name] = append(emits[emit.Name], emit)
		}
	}

	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}

		for _, sub := range m.Manifest.Subscribes {
			problem := EventSubscriptionProblem(sub, emits[sub.Name])
			if problem == "" {
				continue
			}

			owner, _, _ := strings.Cut(sub.Name, ".")
			if slices.Contains(m.Manifest.SoftDependsOn, owner) {
				log.Warn().
					Str("module", name).
					Str("event", sub.Name).
					Int("version", sub.EffectiveVersion()).
					Msg("subscribes to an event version no loaded module emits; owning module is a soft dependency")
				continue
			}

			m.Fail(problem)
			break
		}
	}
}

// EventSubscriptionProblem describes why sub names an event version no
// loaded module emits, given the loaded modules' emits declarations for
// sub's event name, or returns "" when the subscription is satisfied.
func EventSubscriptionProblem(sub manifest.EventSubscription, emitted []manifest.EventDeclaration) string {
	if len(emitted) == 0 {
		return fmt.Sprintf("subscribes to unknown event %q", sub.Name)
	}
	if !slices.ContainsFunc(emitted, func(e manifest.EventDeclaration) bool { return e.EffectiveVersion() == sub.EffectiveVersion() }) {
		return fmt.Sprintf("subscribes to event %q version %d, which no loaded module emits", sub.Name, sub.EffectiveVersion())
	}
	return ""
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

// verifyBundle requires declared frontend bundles to be nonempty and match the manifest's
// SHA-256 digest. Undeclared bundle bytes are ignored.
func verifyBundle(mf *manifest.Manifest, bundleBytes []byte) error {
	if mf.Frontend == nil || mf.Frontend.Bundle == nil || !*mf.Frontend.Bundle {
		return nil
	}
	if len(bundleBytes) == 0 {
		return fmt.Errorf("frontend.bundle is true but the package has no frontend bundle")
	}
	return verifyChecksum(mf.Frontend.BundleSHA256, bundleBytes)
}

func callGetRoutes(ctx context.Context, inst *wasm.ModuleInstance) ([]abiv1.RouteDeclaration, error) {
	data, err := inst.InvokeNoArg(ctx, "get_routes")
	if err != nil {
		return nil, err
	}
	var routes []abiv1.RouteDeclaration
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
// registering routes — l10n, bridge, theme, report_bundle, automation,
// and field_extension all forbid it. Detecting a violation needs the
// actual get_routes() result, not a manifest field alone (there's no
// manifest routes key to check), which is why this lives in the loader
// package rather than alongside manifest.validateModuleType's other
// per-type checks.
func validateModuleRoutes(mf *manifest.Manifest, routes []abiv1.RouteDeclaration) error {
	switch mf.Type {
	case "l10n", "bridge", "theme", "report_bundle", "automation", "field_extension":
		if len(routes) > 0 {
			return fmt.Errorf("type %q must not register routes, got %d", mf.Type, len(routes))
		}
	}
	return nil
}

// validateWebhookVerifier enforces that only a connector exports
// handle_webhook_verify (host-abi-reference.md §10b): other module types have
// no inbound webhook endpoint for a verifier to serve.
func validateWebhookVerifier(mf *manifest.Manifest, inst *wasm.ModuleInstance) error {
	if inst.HasWebhookVerifier() && mf.Type != "connector" {
		return fmt.Errorf("type %q must not export handle_webhook_verify", mf.Type)
	}
	return nil
}

// Virtual models require connector ownership and a Create backend for enabled creates.
// ABAC-restricted virtual access is by ID because external pagination cannot enforce those
// row filters.
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

// validateLifecycleEvents rejects an OnCreate/OnUpdate/OnDelete event whose
// payload names a field the model doesn't have, and any such event on a
// Virtual or Transient model, whose writes never reach the SQL-backed
// emit path (go-sdk-reference.md §7 "Declaring emission on a model").
func validateLifecycleEvents(models []model.ModelDeclaration) error {
	for _, md := range models {
		for _, decl := range []struct {
			modifier string
			event    *model.LifecycleEvent
		}{{"OnCreate", md.OnCreateEvent}, {"OnUpdate", md.OnUpdateEvent}, {"OnDelete", md.OnDeleteEvent}} {
			if decl.event == nil {
				continue
			}
			if md.Backend != "" {
				return fmt.Errorf("model %s: %s is not valid on a %s model, which has no Postgres table", md.Name, decl.modifier, md.Backend)
			}
			for _, field := range decl.event.Fields {
				if !slices.ContainsFunc(md.Fields, func(f model.NamedField) bool { return f.Name == field.Record }) {
					return fmt.Errorf("model %s: %s event %s payload field %q reads %q, which is not a field of the model", md.Name, decl.modifier, decl.event.Name, field.Name, field.Record)
				}
			}
		}
	}
	return nil
}

// validateTrackedFields enforces .Tracked()'s load-time rules
// (record-activity.md §4). A tracked field needs a column of its own to
// record an old and new value from, so One2Many and non-stored computed
// fields are rejected, as is any tracked field on a Virtual or Transient
// model, which has no Postgres table for writes to be captured from. A
// model with a tracked field needs a single UUID primary key, since
// record_activity.record_id is a UUID.
func validateTrackedFields(models []model.ModelDeclaration) error {
	for _, md := range models {
		tracked := false
		for _, f := range md.Fields {
			if !f.Def.IsTracked {
				continue
			}
			tracked = true
			if f.Def.Kind == model.KindOne2Many {
				return fmt.Errorf("model %s: field %s: .Tracked() is not valid on a One2Many field, which has no column of its own", md.Name, f.Name)
			}
			if f.Def.IsComputed && !f.Def.IsStored {
				return fmt.Errorf("model %s: field %s: .Tracked() is not valid on a non-stored computed field, which has no column of its own", md.Name, f.Name)
			}
		}
		if !tracked {
			continue
		}
		if md.Backend != "" {
			return fmt.Errorf("model %s: .Tracked() fields are not valid on a %s model, which has no Postgres table to capture changes from", md.Name, md.Backend)
		}
		var pks []model.NamedField
		for _, f := range md.Fields {
			if f.Def.IsPrimaryKey {
				pks = append(pks, f)
			}
		}
		if len(pks) != 1 || pks[0].Def.Kind != model.KindUUID {
			return fmt.Errorf("model %s: .Tracked() fields require a single UUID primary key", md.Name)
		}
	}
	return nil
}

// validateReservedTableNames rejects a Postgres-backed model whose table
// name module SQL may never reference (dbscope.IsReservedTableName): an
// engine-owned per-tenant table or a partition name of one, which would
// share the tenant schema with the model's table, a pg_* name, which
// resolves to the system catalog ahead of the tenant schema, or a river_*
// name, which module SQL can't reach.
func validateReservedTableNames(models []model.ModelDeclaration) error {
	for _, md := range models {
		if md.Backend != "" {
			continue
		}
		if table := modeltable.Name(md); dbscope.IsReservedTableName(table) {
			return fmt.Errorf("model %s: table name %q is reserved", md.Name, table)
		}
	}
	return nil
}

// Workflow states must match a Selection field's values and action names must be unique.
// Conditions are syntax-checked at load time without enforcement during transitions.
func validateWorkflowTransitions(models []model.ModelDeclaration) error {
	for _, md := range models {
		actionNames := make(map[string]string, 4) // action name -> field name that claimed it
		for _, f := range md.Fields {
			if len(f.Def.WorkflowTransitions) == 0 {
				continue
			}
			if f.Def.Kind != model.KindSelection {
				return fmt.Errorf("model %s: field %s: .Workflow() is only valid on a Selection field", md.Name, f.Name)
			}

			states := make(map[string]bool, len(f.Def.SelectionValues))
			for _, v := range f.Def.SelectionValues {
				states[v] = true
			}

			for _, t := range f.Def.WorkflowTransitions {
				if t.ActionName == "" {
					return fmt.Errorf("model %s: field %s: a workflow transition needs a non-empty action name", md.Name, f.Name)
				}
				if strings.Contains(t.ActionName, "/") {
					return fmt.Errorf("model %s: field %s: transition action name %q must not contain %q — it becomes a single path segment (POST {plural}/{id}/{action_name})", md.Name, f.Name, t.ActionName, "/")
				}
				if !states[t.From] {
					return fmt.Errorf("model %s: field %s: transition %q: from state %q is not one of the field's Selection values", md.Name, f.Name, t.ActionName, t.From)
				}
				if !states[t.To] {
					return fmt.Errorf("model %s: field %s: transition %q: to state %q is not one of the field's Selection values", md.Name, f.Name, t.ActionName, t.To)
				}
				if claimant, ok := actionNames[t.ActionName]; ok {
					return fmt.Errorf("model %s: fields %s and %s both declare a workflow transition named %q", md.Name, claimant, f.Name, t.ActionName)
				}
				actionNames[t.ActionName] = f.Name

				if t.ConditionExpr != "" {
					if _, err := domain.Parse(t.ConditionExpr); err != nil {
						return fmt.Errorf("model %s: field %s: transition %q: condition failed to parse: %w", md.Name, f.Name, t.ActionName, err)
					}
				}
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

func validateSequenceFormats(models []model.ModelDeclaration) error {
	for _, md := range models {
		for _, f := range md.Fields {
			if f.Def.Kind != model.KindSequence {
				continue
			}
			if err := orm.ValidateSequenceFormat(f.Def.SequenceFormat); err != nil {
				return fmt.Errorf("model %s: field %s: %w", md.Name, f.Name, err)
			}
		}
	}
	return nil
}
