package module

import (
	"fmt"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/tetratelabs/wazero"
)

type LoadedModule struct {
	Manifest       manifest.Manifest
	CompiledModule wazero.CompiledModule
	Pool           *wasm.InstancePool
	Status         ModuleStatus
	Capabilities   abi.CapabilitySet
	// PackagePath is where the module was loaded from on disk: a .erp
	// package file, or a loose module directory. Empty only for a
	// synthetically-constructed LoadedModule with no real backing path.
	// Lets later code (e.g. notification template resolution) re-open
	// the module's package and extract a file the initial load didn't —
	// see loader.Source.PackagePath, which this is copied from.
	PackagePath string
	// FailureReason is set only when Status == StatusFailed. Set it via
	// Fail or FailDependency rather than assigning directly, so the two
	// fields can never go out of sync.
	FailureReason string
	LoadedAt      time.Time
	SchemaVersion string
	TenantSyncs   map[string]SchemaSyncStatus
	// LoadOrder is this module's index in the engine's dependency-ordered
	// load sequence (moduleboot.Order) — dependencies always get a lower
	// index than the modules that depend on them. Exposed in /_meta/schema
	// so the shell can apply view extensions dependencies-first without
	// re-deriving the dependency graph itself.
	LoadOrder int

	ExplicitRoutes []abiv1.RouteDeclaration
	ModelDecls     []model.ModelDeclaration
	TypeDecls      []model.TypeDeclaration
	DataMigrations []model.DataMigration
	// NotifTemplates holds this module's resolved notification templates
	// (notiftemplate.Load, called from moduleboot.LoadCascading), nil if
	// the manifest declares no notification_types.
	NotifTemplates *notiftemplate.ModuleTemplates
}

// Fail marks the module as failed for its own load; compile error, invalid
// manifest, checksum mismatch, route conflict, etc.
func (m *LoadedModule) Fail(reason string) {
	m.Status = StatusFailed
	m.FailureReason = reason
}

// FailDependency marks the module as failed because a hard dependency
// (named by upstream) failed to load, distinguishing a cascaded failure from
// one this module caused itself.
func (m *LoadedModule) FailDependency(upstream string) {
	m.Status = StatusFailed
	m.FailureReason = fmt.Sprintf("skipped: depends on failed module '%s'", upstream)
}
