package tenantoffboard

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/search"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/rs/zerolog/log"
)

// Activities implements every activity OffboardTenantWorkflow calls, registered as a
// whole via worker.RegisterActivity(a *Activities) — each exported method
// becomes an activity named after itself (e.g. "MarkOffboarding"). The
// immediate-offboard River job (job.go) calls these same methods directly,
// with no Temporal wrapper — one implementation, two callers.
type Activities struct {
	tenantStore *tenant.Store
	filesStore  *files.Store

	// cacheClient is Redis, fail-hard constructed in Engine.New (Stage 1)
	// — never nil in a running engine, no nil-guard needed. searchClient
	// and storageBackend are both warn-only constructed (engine-internals.md
	// §2) and can legitimately be nil; every method touching them
	// nil-guards first, same convention as httpx's health checks.
	cacheClient    *cache.Client
	searchClient   *search.Client
	storageBackend storage.Backend

	// schemaSyncPool is the same pool tenantprovision.Activities uses for
	// every DDL statement — DROP SCHEMA here, CREATE SCHEMA there.
	schemaSyncPool *sql.DB

	registry *registry.ModuleRegistry
}

func NewActivities(
	tenantStore *tenant.Store,
	filesStore *files.Store,
	cacheClient *cache.Client,
	searchClient *search.Client,
	storageBackend storage.Backend,
	schemaSyncPool *sql.DB,
	moduleRegistry *registry.ModuleRegistry,
) *Activities {
	return &Activities{
		tenantStore:    tenantStore,
		filesStore:     filesStore,
		cacheClient:    cacheClient,
		searchClient:   searchClient,
		storageBackend: storageBackend,
		schemaSyncPool: schemaSyncPool,
		registry:       moduleRegistry,
	}
}

// MarkOffboarding transitions the tenant from active to offboarding
// (tenant.Store.BeginOffboarding's own CAS guard).
func (a *Activities) MarkOffboarding(ctx context.Context, slug string) error {
	if _, err := a.tenantStore.BeginOffboarding(ctx, slug); err != nil {
		return fmt.Errorf("mark offboarding: %w", err)
	}
	return nil
}

// MarkDeletionStarted is the cancellation cutoff: it CAS-sets
// offboard_deletion_started_at, racing against CancelOffboard's own CAS
// update on the same guard (tenant.Store.MarkDeletionStarted's doc
// comment). started reports whether this call won that race — false
// means CancelOffboard got there first, and OffboardTenantWorkflow must stop without
// running any deletion step.
func (a *Activities) MarkDeletionStarted(ctx context.Context, slug string) (started bool, err error) {
	started, err = a.tenantStore.MarkDeletionStarted(ctx, slug)
	if err != nil {
		return false, fmt.Errorf("mark deletion started: %w", err)
	}
	return started, nil
}

// DeleteSearchIndexes deletes every Meilisearch index declared by a
// currently loaded module, for this tenant (multitenancy-internals.md
// §13's {tenant_id}_{resource} naming). A nil searchClient (Meilisearch
// unconfigured or unreachable at engine startup — warn-only) is not an
// error: there's nothing to delete against, so this is a no-op, logged.
func (a *Activities) DeleteSearchIndexes(ctx context.Context, tenantID string) error {
	if a.searchClient == nil {
		log.Warn().Str("tenantID", tenantID).Msg("offboard: search client unavailable, skipping index deletion")
		return nil
	}

	snap := a.registry.Snapshot()
	if snap == nil {
		return nil
	}

	for _, mod := range snap.Modules() {
		if mod.Status == module.StatusFailed {
			continue
		}
		for _, idx := range mod.Manifest.SearchIndexes {
			indexUID := tenantID + "_" + idx.Name
			if err := a.searchClient.DeleteIndex(indexUID); err != nil {
				return fmt.Errorf("delete search index %q: %w", indexUID, err)
			}
		}
	}

	return nil
}

// FlushTenantCache removes every Redis key under this tenant's prefix
// (multitenancy-internals.md §12's {tenant_id}: key structure).
func (a *Activities) FlushTenantCache(ctx context.Context, tenantID string) error {
	if err := a.cacheClient.DeleteByPrefix(ctx, tenantID+":"); err != nil {
		return fmt.Errorf("flush tenant cache: %w", err)
	}
	return nil
}

// DeleteTenantStorageFiles deletes every object storage file the tenant's
// `files` table has ever recorded (files.Store.StorageKeysForTenant's own
// doc comment explains why that table, not a prefix scan, is the only way
// to enumerate them). A nil storageBackend (warn-only) is a no-op, logged,
// same as DeleteSearchIndexes. One file's delete failing doesn't stop the
// rest — every key is attempted, and every failure is collected into the
// returned error so the caller sees the full picture, not just the first
// one.
func (a *Activities) DeleteTenantStorageFiles(ctx context.Context, slug string) error {
	if a.storageBackend == nil {
		log.Warn().Str("slug", slug).Msg("offboard: storage backend unavailable, skipping storage file deletion")
		return nil
	}

	keys, err := a.filesStore.StorageKeysForTenant(ctx, slug)
	if err != nil {
		return fmt.Errorf("list storage keys for tenant %q: %w", slug, err)
	}

	var firstErr error
	failed := 0
	for _, key := range keys {
		if err := a.storageBackend.Delete(ctx, key); err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			log.Error().Err(err).Str("slug", slug).Str("key", key).Msg("offboard: failed to delete storage file")
		}
	}
	if firstErr != nil {
		return fmt.Errorf("delete %d/%d storage files for tenant %q, first error: %w", failed, len(keys), slug, firstErr)
	}

	return nil
}

// DropTenantSchema drops tenant_{slug} and everything in it — the point
// of no return (multitenancy-internals.md §9). IF EXISTS makes this
// idempotent against a workflow retry that reaches here twice.
func (a *Activities) DropTenantSchema(ctx context.Context, slug string) error {
	if _, err := a.schemaSyncPool.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+tenantschema.Name(slug)+" CASCADE"); err != nil {
		return fmt.Errorf("drop tenant schema: %w", err)
	}
	return nil
}

// MarkTenantDeleted flips the tenant to StatusDeleted — tenant.Store.
// UpdateStatus's own set-once deleted_at handling. The tenants row itself
// is retained indefinitely (multitenancy-internals.md §9), not removed.
func (a *Activities) MarkTenantDeleted(ctx context.Context, slug string) error {
	if _, err := a.tenantStore.UpdateStatus(ctx, slug, tenant.StatusDeleted, nil); err != nil {
		return fmt.Errorf("mark tenant deleted: %w", err)
	}
	return nil
}
