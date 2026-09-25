package engine

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auditlog"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/operatorcert"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

// bootstrapSystemSchema creates the system-schema tables, in foreign-key
// order, as the schema-sync role, which owns them (data-layer.md §2.2).
func bootstrapSystemSchema(ctx context.Context, schemaPool *sql.DB, syncPool *schema.SchemaSyncPool) error {
	steps := []struct {
		name      string
		bootstrap func(context.Context) error
	}{
		{"schema sync tracking table", syncPool.Bootstrap},
		{"tenant registry", tenant.NewStore(schemaPool).Bootstrap},
		{"billing schema", billing.NewStore(schemaPool).Bootstrap},
		{"checkpoint schema", checkpoint.NewStore(schemaPool).Bootstrap},
		{"user identity store", user.NewStore(schemaPool).Bootstrap},
		{"api key store", apikey.NewStore(schemaPool).Bootstrap},
		{"mfa credential store", mfa.NewStore(schemaPool).Bootstrap},
		{"row encryption keys table", rowcrypt.NewStore(schemaPool, nil).Bootstrap},
		{"tenant config overrides table", tenantconfig.NewStore(schemaPool).Bootstrap},
		{"auth audit log", authaudit.NewStore(schemaPool, nil).Bootstrap},
		{"admin audit log", auditlog.NewStore(schemaPool).Bootstrap},
		{"operator certificate ledger", operatorcert.NewStore(schemaPool).Bootstrap},
		{"sessions table", session.NewStore(schemaPool).Bootstrap},
		{"jwt signing keys table", signingkey.NewStore(schemaPool, nil).Bootstrap},
		{"mfa token signing keys table", mfatoken.NewStore(schemaPool, nil).Bootstrap},
	}
	for _, s := range steps {
		if err := s.bootstrap(ctx); err != nil {
			return fmt.Errorf("bootstrap %s: %w", s.name, err)
		}
	}
	return nil
}

// migrateJobQueue applies River's migrations as the schema-sync role.
func migrateJobQueue(ctx context.Context, schemaSyncDSN string) error {
	pool, err := db.NewPgxPool(ctx, schemaSyncDSN)
	if err != nil {
		return fmt.Errorf("connect job queue migration pool: %w", err)
	}
	defer pool.Close()
	if err := jobqueue.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate job queue schema: %w", err)
	}
	return nil
}
