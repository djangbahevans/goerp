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
	Diagnostics    []manifest.Diagnostic
	Manifest       manifest.Manifest
	CompiledModule wazero.CompiledModule
	Pool           *wasm.InstancePool
	Status         ModuleStatus
	Capabilities   abi.CapabilitySet
	// PackagePath lets template resolution reopen assets omitted from the initial load.
	PackagePath string
	// Fail and FailDependency keep status and reason consistent.
	FailureReason string
	LoadedAt      time.Time
	SchemaVersion string
	TenantSyncs   map[string]SchemaSyncStatus
	// The shell applies view extensions in this dependency order.
	LoadOrder int

	ExplicitRoutes     []abiv1.RouteDeclaration
	ModelDecls         []model.ModelDeclaration
	ModelExtensions    []model.ModelExtension
	TypeDecls          []model.TypeDeclaration
	DataMigrations     []model.DataMigration
	HasWebhookVerifier bool
	NotifTemplates     *notiftemplate.ModuleTemplates
	// Resolved references use the full module.key as their map key.
	UsesConfig map[string]manifest.UsesConfigEntry
	ErrorCodes map[string]struct{}
}

func (m *LoadedModule) Fail(reason string) {
	m.Status = StatusFailed
	m.FailureReason = reason
}

func (m *LoadedModule) FailDependency(upstream string) {
	m.Status = StatusFailed
	m.FailureReason = fmt.Sprintf("skipped: depends on failed module '%s'", upstream)
}
