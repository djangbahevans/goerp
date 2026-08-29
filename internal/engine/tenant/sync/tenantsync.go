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
	"context"
	"fmt"
	"sync"

	"github.com/djangbahevans/goerp/internal/engine/module"
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
		syncModule(ctx, pool, diffEngine, tenants, mod, concurrency)
	}

	return nil
}

func syncModule(ctx context.Context, pool *schema.SchemaSyncPool, diffEngine *schema.SchemaDiffEngine, tenants []tenant.Tenant, mod *module.LoadedModule, concurrency int) {
	fanOut(tenants, concurrency, func(t tenant.Tenant) {
		if err := SyncOne(ctx, pool, diffEngine, t, mod, nil); err != nil {
			log.Error().Err(err).
				Str("tenant", t.Slug).
				Str("module", mod.Manifest.Name).
				Msg("schema sync failed")
		}
	})
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
		wg.Add(1)
		go func(item T) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(item)
		}(item)
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

	// appliedHashes' own consumption is handled atomically inside
	// ExecuteAccepted/applyChanges (same transaction as the DDL) — see
	// apply.go's own doc comment for why that can't safely be a separate,
	// later call from here.
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

	if err := sess.RecordSyncSuccess(ctx); err != nil {
		return fmt.Errorf("record sync success: %w", err)
	}

	return nil
}
