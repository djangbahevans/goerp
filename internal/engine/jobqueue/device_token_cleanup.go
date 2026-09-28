package jobqueue

import (
	"context"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

// DeviceTokenCleanupArgs is the platform-wide periodic job that deletes
// push device tokens unseen for notifications.DeviceTokenStaleAfter
// (notification-system.md §12) — registered as a daily river.PeriodicJob
// in New, not inserted by any caller. A single run fans out across every
// active tenant itself, the same shape as InviteExpiryArgs.
type DeviceTokenCleanupArgs struct{}

func (DeviceTokenCleanupArgs) Kind() string { return "device_token_cleanup" }

func (DeviceTokenCleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAdmin}
}

type DeviceTokenCleanupWorker struct {
	river.WorkerDefaults[DeviceTokenCleanupArgs]
	TenantStore       *tenant.Store
	NotificationStore *notifications.Store
}

// Work deletes each active tenant's stale tokens independently, logging
// (not aborting on) a single tenant's failure, as InviteExpiryWorker does.
func (w *DeviceTokenCleanupWorker) Work(ctx context.Context, job *river.Job[DeviceTokenCleanupArgs]) error {
	tenants, err := w.TenantStore.ActiveTenants(ctx)
	if err != nil {
		return fmt.Errorf("list active tenants: %w", err)
	}

	cutoff := time.Now().Add(-notifications.DeviceTokenStaleAfter)
	for _, t := range tenants {
		n, err := w.NotificationStore.DeleteStaleDeviceTokens(ctx, t.Slug, cutoff)
		if err != nil {
			log.Error().Err(err).Str("tenant", t.Slug).Msg("device token cleanup: tenant failed")
			continue
		}
		if n > 0 {
			log.Info().Int64("deleted", n).Str("tenant", t.Slug).Msg("device token cleanup: deleted stale tokens")
		}
	}
	return nil
}
