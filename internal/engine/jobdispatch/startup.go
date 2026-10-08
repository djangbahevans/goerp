package jobdispatch

import (
	"context"
	"fmt"
	"sync"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

// startupSweepConcurrency bounds how many (module, tenant) pairs this
// sweep evaluates at once — not tenantsync.DefaultConcurrency's own value
// reused directly (importing tenantsync here would cycle back through it
// importing this package), but the same default width.
const startupSweepConcurrency = 8

// EnqueueStartupDataMigrations runs after queue construction because startup schema sync
// happens before jobs can be inserted. Handler uniqueness makes repeated sweeps safe.
func EnqueueStartupDataMigrations(ctx context.Context, riverClient *river.Client[pgx.Tx], pool *schema.SchemaSyncPool, tenantStore *tenant.Store, modules []*module.LoadedModule) error {
	tenants, err := tenantStore.ActiveTenants(ctx)
	if err != nil {
		return fmt.Errorf("enumerate active tenants: %w", err)
	}

	sem := make(chan struct{}, startupSweepConcurrency)
	var wg sync.WaitGroup

	for _, mod := range modules {
		if mod.Status == module.StatusFailed || len(mod.DataMigrations) == 0 {
			continue
		}
		for _, t := range tenants {
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()

				if err := EnqueueApplicableDataMigration(ctx, riverClient, pool, t.ID, mod); err != nil {
					// An enqueue failure for one tenant/module pair must not block the
					// remaining pairs.
					log.Error().Err(err).Str("module", mod.Manifest.Name).Str("tenant", t.Slug).
						Msg("startup: failed to enqueue data migration")
				}
			})
		}
	}
	wg.Wait()

	return nil
}
