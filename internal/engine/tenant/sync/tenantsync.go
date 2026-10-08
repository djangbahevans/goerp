// Package tenantsync runs Stage 4 of the engine startup sequence
// (engine-internals.md §2): for each loaded module × each active tenant,
// open a SchemaSyncSession, diff the module's declared models against the
// tenant's live schema, execute safe DDL, and record the result. Skipping
// per-(tenant, module) sync when already synced to the current version,
// bounding and parallelizing across tenants, and never letting one
// tenant's failure block another's, are this package's whole job —
// discovering which modules to sync and in what order remains the
// caller's responsibility, same as loader.LoadAll's Source ordering.
package tenantsync

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/djangbahevans/goerp/internal/engine/cronsettings"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/rs/zerolog/log"
)

// DefaultConcurrency is GOERP_SCHEMA_SYNC_CONCURRENCY's documented default
// (engine-internals.md §2 Stage 4; multitenancy-internals.md §16).
const DefaultConcurrency = 8

// SyncAll enumerates active tenants from tenantStore, then runs schema
// sync for every (module, tenant) pair — modules in the order given
// (already dependency-ordered by the caller), tenants within each module
// bounded to concurrency concurrent syncs (DefaultConcurrency if <= 0). A
// module with Status StatusFailed is skipped entirely — nothing to sync
// against a module that never finished loading. Returns an error only if
// active tenants can't be enumerated at all; a per-tenant sync failure is
// logged and never aborts the run.
func SyncAll(ctx context.Context, pool *schema.SchemaSyncPool, diffEngine *schema.SchemaDiffEngine, tenantStore *tenant.Store, modules []*module.LoadedModule, concurrency int) error {
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}

	tenants, err := tenantStore.ActiveTenants(ctx)
	if err != nil {
		return fmt.Errorf("enumerate active tenants: %w", err)
	}

	for _, mod := range modules {
		if mod.Status == module.StatusFailed {
			continue
		}
		syncModuleTenants(ctx, pool, diffEngine, tenants, mod, concurrency)
	}

	return nil
}

// TenantSyncResult is one tenant's outcome from SyncModule — its Err is
// nil on success.
type TenantSyncResult struct {
	Tenant tenant.Tenant
	Err    error
}

// SyncModuleResult aggregates SyncModule's per-tenant outcomes, so a
// caller that needs to know which specific tenants failed (and why) — not
// just whether any did — has that without re-deriving it from logs.
type SyncModuleResult struct {
	Succeeded []tenant.Tenant
	Failed    []TenantSyncResult
}

// SyncModule enumerates active tenants from tenantStore and syncs mod
// against every one of them, bounded to concurrency concurrent syncs
// (DefaultConcurrency if <= 0) — the same fan-out SyncAll uses internally
// for its whole-batch startup sweep, but scoped to one module and
// returning a SyncModuleResult instead of only logging. A failing
// tenant's sync is still logged (as SyncAll's is) and never stops or
// delays another tenant's sync; the difference is purely that the
// per-tenant outcome is also handed back to the caller, for a caller like
// module install/upgrade orchestration that must not mark a module READY
// until it knows exactly which tenants are actually synced.
func SyncModule(ctx context.Context, pool *schema.SchemaSyncPool, diffEngine *schema.SchemaDiffEngine, tenantStore *tenant.Store, mod *module.LoadedModule, concurrency int) (SyncModuleResult, error) {
	tenants, err := tenantStore.ActiveTenants(ctx)
	if err != nil {
		return SyncModuleResult{}, fmt.Errorf("enumerate active tenants: %w", err)
	}

	return SyncModuleTenants(ctx, pool, diffEngine, tenants, mod, concurrency), nil
}

// SyncModuleTenants is SyncModule, but for a caller that already has its
// own tenants slice in hand (e.g. one that also needs it for a check that
// has to run before sync, like a downgrade pre-check) and would otherwise
// pay a second identical ActiveTenants round trip for no new information.
func SyncModuleTenants(ctx context.Context, pool *schema.SchemaSyncPool, diffEngine *schema.SchemaDiffEngine, tenants []tenant.Tenant, mod *module.LoadedModule, concurrency int) SyncModuleResult {
	return syncModuleTenants(ctx, pool, diffEngine, tenants, mod, concurrency)
}

func syncModuleTenants(ctx context.Context, pool *schema.SchemaSyncPool, diffEngine *schema.SchemaDiffEngine, tenants []tenant.Tenant, mod *module.LoadedModule, concurrency int) SyncModuleResult {
	var mu sync.Mutex
	var result SyncModuleResult

	fanOut(tenants, concurrency, func(t tenant.Tenant) {
		err := SyncOne(ctx, pool, diffEngine, t, mod, nil)

		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			log.Error().Err(err).
				Str("tenant", t.Slug).
				Str("module", mod.Manifest.Name).
				Msg("schema sync failed")
			result.Failed = append(result.Failed, TenantSyncResult{Tenant: t, Err: err})
			return
		}
		result.Succeeded = append(result.Succeeded, t)
	})

	// Concurrent completion order is arbitrary; sort so identical calls return identical results.
	slices.SortFunc(result.Succeeded, func(a, b tenant.Tenant) int { return cmp.Compare(a.Slug, b.Slug) })
	slices.SortFunc(result.Failed, func(a, b TenantSyncResult) int { return cmp.Compare(a.Tenant.Slug, b.Tenant.Slug) })

	return result
}

// fanOut runs fn for each item in items with at most concurrency running
// at once (DefaultConcurrency if <= 0), waiting for all to finish before
// returning — the shared bounded semaphore+WaitGroup shape behind
// syncModule, Admin.Status's pending-filter sweep, and SyncWorker.run,
// each fanning a "one unit of work, many items" shape across
// (tenant, module) pairs. fn is responsible for its own synchronization
// if it accumulates results into shared state.
func fanOut[T any](items []T, concurrency int, fn func(T)) {
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, item := range items {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(item)
		})
	}

	wg.Wait()
}

// SyncOne runs schema sync for a single (tenant, module) pair — the same
// logic SyncAll fans out across every active tenant, callable directly by
// a caller with exactly one tenant already in hand and no need to go
// through ActiveTenants (e.g. goerp#149's provisioning workflow, syncing
// a tenant that's still StatusProvisioning and therefore not yet
// "active" — ActiveTenants wouldn't return it at all). accepted applies
// any blocked change whose schema.ChangeHash it contains — goerp#292's
// `schema accept`-triggered one-time resync, the only caller that ever
// passes a non-empty map; nil (every other caller) applies only the safe/
// automatic class of change. Once applied, a change stops appearing in a
// later Diff at all (the live schema now matches), so accepted hashes
// never need to be "consumed" or expire on their own.
func SyncOne(ctx context.Context, pool *schema.SchemaSyncPool, diffEngine *schema.SchemaDiffEngine, t tenant.Tenant, mod *module.LoadedModule, accepted map[string]bool) error {
	if err := cronsettings.NewStore(pool.Raw()).Initialize(ctx, t.Slug, mod.Manifest.Name, mod.Manifest.CronJobs); err != nil {
		return fmt.Errorf("initialize cron choices: %w", err)
	}

	sess, err := pool.BeginSync(ctx, t.ID, t.Slug, mod.Manifest.Name, &mod.Manifest)
	if err != nil {
		return fmt.Errorf("begin sync session: %w", err)
	}
	defer func() {
		if err := sess.Close(ctx); err != nil {
			log.Warn().Err(err).
				Str("tenant", t.Slug).
				Str("module", mod.Manifest.Name).
				Msg("could not close schema sync session")
		}
	}()

	needsSync, err := sess.NeedsSync(ctx)
	if err != nil {
		return fmt.Errorf("check sync need: %w", err)
	}
	if !needsSync && len(accepted) == 0 {
		return nil
	}

	changes, err := diffEngine.Diff(ctx, sess, mod.ModelDecls, mod.TypeDecls)
	if err != nil {
		if recErr := sess.RecordSyncFailure(ctx); recErr != nil {
			log.Warn().Err(recErr).Str("tenant", t.Slug).Str("module", mod.Manifest.Name).Msg("could not record sync failure")
		}
		return fmt.Errorf("diff schema: %w", err)
	}

	_, _, err = diffEngine.ExecuteAccepted(ctx, sess, mod.ModelDecls, changes, accepted)
	if err != nil {
		if recErr := sess.RecordSyncFailure(ctx); recErr != nil {
			log.Warn().Err(recErr).Str("tenant", t.Slug).Str("module", mod.Manifest.Name).Msg("could not record sync failure")
		}
		return fmt.Errorf("execute DDL: %w", err)
	}

	if err := diffEngine.SyncRLSPolicies(ctx, sess, mod.ModelDecls, mod.Manifest.Policies); err != nil {
		if recErr := sess.RecordSyncFailure(ctx); recErr != nil {
			log.Warn().Err(recErr).Str("tenant", t.Slug).Str("module", mod.Manifest.Name).Msg("could not record sync failure")
		}
		return fmt.Errorf("sync RLS policies: %w", err)
	}

	if err := diffEngine.SyncEtagTriggers(ctx, sess, mod.ModelDecls, mod.Manifest.AuditedTables); err != nil {
		if recErr := sess.RecordSyncFailure(ctx); recErr != nil {
			log.Warn().Err(recErr).Str("tenant", t.Slug).Str("module", mod.Manifest.Name).Msg("could not record sync failure")
		}
		return fmt.Errorf("sync etag triggers: %w", err)
	}

	// After RLS policies, so module SQL never reaches a table before its
	// policies are in place.
	if err := diffEngine.SyncTenantRoleGrants(ctx, sess, mod.ModelDecls); err != nil {
		if recErr := sess.RecordSyncFailure(ctx); recErr != nil {
			log.Warn().Err(recErr).Str("tenant", t.Slug).Str("module", mod.Manifest.Name).Msg("could not record sync failure")
		}
		return fmt.Errorf("sync tenant role grants: %w", err)
	}

	if err := seedNotificationTemplates(ctx, pool, t.Slug, mod); err != nil {
		if recErr := sess.RecordSyncFailure(ctx); recErr != nil {
			log.Warn().Err(recErr).Str("tenant", t.Slug).Str("module", mod.Manifest.Name).Msg("could not record sync failure")
		}
		return err
	}

	// Reapply default grants only on version sync, preserving admin removals between upgrades.
	if err := role.NewStore(pool.Raw()).GrantModuleDefaults(ctx, t.Slug, mod.Manifest.Permissions); err != nil {
		if recErr := sess.RecordSyncFailure(ctx); recErr != nil {
			log.Warn().Err(recErr).Str("tenant", t.Slug).Str("module", mod.Manifest.Name).Msg("could not record sync failure")
		}
		return fmt.Errorf("grant default permissions: %w", err)
	}

	if err := sess.RecordSyncSuccess(ctx); err != nil {
		return fmt.Errorf("record sync success: %w", err)
	}

	return nil
}

// seedNotificationTemplates makes mod's default notification_templates
// rows in t's schema match the templates its package ships, leaving
// tenant overrides alone. A variant that cannot be stored as columns is
// logged and skipped rather than failing the sync, and then no default is
// deleted, so a variant that used to store keeps its row.
func seedNotificationTemplates(ctx context.Context, pool *schema.SchemaSyncPool, tenantSlug string, mod *module.LoadedModule) error {
	store := notifications.NewStore(pool.Raw())
	seed := store.SeedDefaultTemplates

	rows, err := mod.NotifTemplates.Rows(mod.Manifest.Name)
	if err != nil {
		log.Warn().Err(err).Str("tenant", tenantSlug).Str("module", mod.Manifest.Name).Msg("some notification templates cannot be stored as notification_templates rows")
		seed = store.UpsertDefaultTemplates
	}

	if err := seed(ctx, tenantSlug, mod.Manifest.Name, rows); err != nil {
		return fmt.Errorf("seed notification templates: %w", err)
	}

	return nil
}
