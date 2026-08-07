package schema

import (
	"context"
	"time"

	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type Config struct {
	DDLStatementTimeout time.Duration
}

type SchemaDiffEngine struct {
	cfg *Config
}

func NewSchemaDiffEngine(cfg *Config) *SchemaDiffEngine {
	return &SchemaDiffEngine{cfg: cfg}
}

func (e *SchemaDiffEngine) Diff(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration) ([]schema.Change, error) {
	decl, err := newModuleSchemaDeclaration("tenant_"+sess.tenantSlug, sess.moduleName, modelDecls)
	if err != nil {
		return nil, err
	}

	driver, err := postgres.Open(sess.conn)
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
