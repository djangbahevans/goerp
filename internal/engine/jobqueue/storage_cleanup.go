package jobqueue

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

type StorageCleanupArgs struct{}

func (StorageCleanupArgs) Kind() string { return "storage_cleanup" }

func (StorageCleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAdmin}
}

type StorageCleanupWorker struct {
	river.WorkerDefaults[StorageCleanupArgs]
	TenantStore *tenant.Store
	FileStore   *files.Store
	Backend     storage.Backend
	Retention   time.Duration
}

func (w *StorageCleanupWorker) Work(ctx context.Context, _ *river.Job[StorageCleanupArgs]) error {
	if w.Backend == nil {
		return fmt.Errorf("storage cleanup requires a storage backend")
	}
	retention := cmp.Or(w.Retention, config.DefaultStorageCleanupRetention)
	if retention <= 0 {
		return fmt.Errorf("storage cleanup retention must be positive")
	}

	tenants, err := w.TenantStore.ActiveTenants(ctx)
	if err != nil {
		return fmt.Errorf("list active tenants: %w", err)
	}

	cutoff := time.Now().Add(-retention)
	for _, t := range tenants {
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := w.cleanupTenant(ctx, t.Slug, cutoff); err != nil {
			log.Error().Err(err).Str("tenant", t.Slug).Msg("storage cleanup: tenant failed")
		}
	}

	return ctx.Err()
}

func (w *StorageCleanupWorker) cleanupTenant(ctx context.Context, slug string, cutoff time.Time) error {
	const batchSize = 100
	var afterID string
	for {
		candidates, err := w.FileStore.DeletedBefore(ctx, slug, cutoff, afterID, batchSize)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}

		for _, candidate := range candidates {
			if err := ctx.Err(); err != nil {
				return err
			}

			if err := w.Backend.Delete(ctx, candidate.StorageKey); err != nil && !errors.Is(err, fs.ErrNotExist) {
				log.Error().Err(err).Str("tenant", slug).Str("file_id", candidate.ID).Msg("storage cleanup: backend deletion failed")
				continue
			}
			if err := w.FileStore.PurgeDeleted(ctx, slug, candidate, cutoff); err != nil {
				log.Error().Err(err).Str("tenant", slug).Str("file_id", candidate.ID).Msg("storage cleanup: metadata deletion failed")
			}
		}

		// Advancing past failed files keeps one failure from stalling this run;
		// their retained rows are eligible again on the next run.
		afterID = candidates[len(candidates)-1].ID
	}
}
