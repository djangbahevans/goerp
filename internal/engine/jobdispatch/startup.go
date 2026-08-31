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

// EnqueueStartupDataMigrations calls EnqueueApplicableDataMigration for
// every loaded module × every active tenant — the Stage 6 counterpart to
// schema.EnqueuePendingValidations, for the identical reason: Stage 4
// schema sync (tenantsync.SyncAll) runs before the job queue client
// exists, so engine startup's own sync pass can't enqueue anything
// directly. Calling this once, right after the job queue client is built,
// catches every tenant/module pair a plain startup (no hot reload, no
// install, no new-tenant provisioning) leaves with an un-advanced
// data_migration_version watermark — including, via
// EnqueueApplicableDataMigration's own idempotent uniqueness, one a crash
// left mid-chain on a previous run. A module with no declared
// DataMigrations, or a tenant already at the module's current version, is
// a no-op per EnqueueApplicableDataMigration's own checks — so this is
// safe to call on every startup regardless of whether anything is
// actually pending.
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
			wg.Add(1)
			go func(mod *module.LoadedModule, t tenant.Tenant) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				if err := EnqueueApplicableDataMigration(ctx, riverClient, pool, t.ID, mod); err != nil {
					// Logged and skipped, not returned: one tenant/module
					// pair's enqueue failure shouldn't block every other
					// pair's, matching tenantsync.SyncAll's own per-tenant
					// failure isolation (§2 Stage 4) one stage earlier in
					// the same startup sequence.
					log.Error().Err(err).Str("module", mod.Manifest.Name).Str("tenant", t.Slug).
						Msg("startup: failed to enqueue data migration")
				}
			}(mod, t)
		}
	}
	wg.Wait()

	return nil
}
