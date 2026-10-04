package tenantsync

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/enginenotif"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/rs/zerolog/log"
)

// SyncEngineNotificationTemplates reconciles embedded defaults on every startup,
// independently of module versions. Tenant failures are logged so other tenants
// can complete startup reconciliation.
func SyncEngineNotificationTemplates(ctx context.Context, pool *schema.SchemaSyncPool, tenantStore *tenant.Store, concurrency int) error {
	rows, err := enginenotif.DefaultRows()
	if err != nil {
		return fmt.Errorf("load engine notification templates: %w", err)
	}

	tenants, err := tenantStore.ActiveTenants(ctx)
	if err != nil {
		return fmt.Errorf("enumerate active tenants: %w", err)
	}

	store := notifications.NewStore(pool.Raw())
	fanOut(tenants, concurrency, func(t tenant.Tenant) {
		if err := store.SeedDefaultTemplates(ctx, t.Slug, enginenotif.Module, rows); err != nil {
			log.Error().Err(err).Str("tenant", t.Slug).Msg("sync engine notification templates failed")
		}
	})

	return nil
}
