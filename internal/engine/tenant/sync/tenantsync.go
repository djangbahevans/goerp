// Package tenantsync applies safe module DDL across active tenants with bounded
// concurrency and per-tenant failure isolation. Callers supply module order.
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
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/rs/zerolog/log"
)

// DefaultConcurrency bounds concurrent tenant schema syncs when no override is supplied.
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

// SyncModule reports outcomes for all active tenants with bounded concurrency. One
// tenant's failure does not abort the other syncs.
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

// SyncOne syncs a tenant/module pair, including a provisioning tenant absent from
// ActiveTenants. Accepted hashes authorize blocked changes for this run; nil permits only
// automatic changes.
func SyncOne(ctx context.Context, pool *schema.SchemaSyncPool, diffEngine *schema.SchemaDiffEngine, t tenant.Tenant, mod *module.LoadedModule, accepted map[string]bool) error {
	var categories []string
	if mod.Manifest.Type == "connector" {
		categories = providerselect.Categories(mod.Manifest.Provides)
	}

	if err := providerselect.NewStore(pool.Raw()).Reconcile(ctx, t.ID, mod.Manifest.Name, categories); err != nil {
		return fmt.Errorf("reconcile provider eligibility: %w", err)
	}

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

	changes, err := diffEngine.Diff(ctx, sess, mod.ModelDecls, mod.TypeDecls, mod.ModelExtensions...)
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

// seedNotificationTemplates reconciles package defaults while preserving tenant overrides.
// Unstorable variants warn and suppress deletion of retained defaults.
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
