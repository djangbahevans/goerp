package schema

import (
	"context"
	"fmt"
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

func (e *SchemaDiffEngine) Diff(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, typeDecls []model.TypeDeclaration, extensions ...model.ModelExtension) ([]schema.Change, error) {
	decl, err := newModuleSchemaDeclaration("tenant_"+sess.tenantSlug, sess.moduleName, modelDecls, typeDecls, e.dependencyOptions(sess.manifest)...)
	if err != nil {
		return nil, err
	}

	driver, err := postgres.Open(sess.execQuerier())
	if err != nil {
		return nil, err
	}

	options := &schema.InspectOptions{Tables: decl.OwnedTables}
	if len(decl.OwnedTables) == 0 {
		options.Mode = schema.InspectTypes | schema.InspectObjects
	}
	if len(decl.OwnedTables) == 0 && len(typeDecls) == 0 {
		return e.diffExtensions(ctx, sess, driver, extensions, typeDecls)
	}
	live, err := driver.InspectSchema(ctx, "tenant_"+sess.tenantSlug, options)
	if err != nil {
		return nil, err
	}

	if err := preserveExtensionObjects(ctx, sess, live, decl.Atlas); err != nil {
		return nil, err
	}
	if sess.manifest.Type == "field_extension" {
		for _, object := range live.Objects {
			enum, ok := object.(*schema.EnumType)
			if !ok {
				continue
			}
			_, declared := decl.Atlas.Object(func(o schema.Object) bool {
				e, ok := o.(*schema.EnumType)
				return ok && e.T == enum.T
			})
			if !declared {
				decl.Atlas.AddObjects(object)
			}
		}
	}
	changes, err := driver.SchemaDiff(live, decl.Atlas)
	if err != nil {
		return nil, err
	}
	if sess.manifest.Type == "field_extension" {
		for _, change := range changes {
			if _, addition := change.(*schema.AddObject); !addition {
				return nil, fmt.Errorf("model extensions only permit additive schema objects, got %T", change)
			}
		}
	}
	extra, err := e.diffExtensions(ctx, sess, driver, extensions, typeDecls)
	if err != nil {
		return nil, err
	}
	return append(changes, extra...), nil
}
