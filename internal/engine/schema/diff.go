package schema

import (
	"context"
	"slices"
	"time"

	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type Config struct {
	DDLStatementTimeout time.Duration

	// ModelSource supplies the model declarations of the modules a synced
	// module depends on, so a Many2One to a hard dependency can reference
	// that model's table. A nil ModelSource leaves every dependency
	// unloaded.
	ModelSource ModelSource

	// TolerateUnloadedDependencies makes a relation to a hard dependency
	// that is not loaded a plain column rather than an error, for a caller
	// that syncs one module in isolation.
	TolerateUnloadedDependencies bool
}

// ModelSource resolves a loaded module's model declarations.
type ModelSource interface {
	ModelDeclarations(moduleName string) ([]model.ModelDeclaration, bool)
}

type SchemaDiffEngine struct {
	cfg *Config
}

func NewSchemaDiffEngine(cfg *Config) *SchemaDiffEngine {
	return &SchemaDiffEngine{cfg: cfg}
}

func (e *SchemaDiffEngine) dependencyOptions(mf *manifest.Manifest) []AtlasOption {
	opts := []AtlasOption{WithDependencies(mf.DependsOn, mf.SoftDependsOn)}
	if e.cfg.TolerateUnloadedDependencies {
		opts = append(opts, WithUnloadedDependenciesTolerated())
	}
	if e.cfg.ModelSource == nil {
		return opts
	}
	models := make(map[string][]model.ModelDeclaration)
	for _, name := range append(slices.Clone(mf.DependsOn), mf.SoftDependsOn...) {
		if decls, ok := e.cfg.ModelSource.ModelDeclarations(name); ok {
			models[name] = decls
		}
	}
	return append(opts, WithDependencyModels(models))
}

func (e *SchemaDiffEngine) Diff(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, typeDecls []model.TypeDeclaration) ([]schema.Change, error) {
	decl, err := newModuleSchemaDeclaration("tenant_"+sess.tenantSlug, sess.moduleName, modelDecls, typeDecls, e.dependencyOptions(sess.manifest)...)
	if err != nil {
		return nil, err
	}

	driver, err := postgres.Open(sess.execQuerier())
	if err != nil {
		return nil, err
	}

	live, err := driver.InspectSchema(ctx, "tenant_"+sess.tenantSlug, &schema.InspectOptions{
		Tables: decl.OwnedTables,
	})
	if err != nil {
		return nil, err
	}

	return driver.SchemaDiff(live, decl.Atlas)
}
