package notifications

import (
	"context"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// DeviceTokensTable is the push device-token table's unqualified name.
const DeviceTokensTable = "user_device_tokens"

// DeviceTokenPlatforms are the platforms a device token can be
// registered for.
var DeviceTokenPlatforms = []string{"ios", "android", "web"}

// DeviceTokenStaleAfter is how long a token can go unseen before
// DeleteStaleDeviceTokens removes it (notification-system.md §12).
const DeviceTokenStaleAfter = 90 * 24 * time.Hour

// RegisterDeviceToken upserts userID's token: a first registration inserts
// it, and a repeat one refreshes last_seen_at and app_version. appVersion
// "" stores NULL. A token belongs to one device, so registering it also
// removes it from any other user in the tenant — the previous user of a
// shared device stops getting pushes on it.
func (s *Store) RegisterDeviceToken(ctx context.Context, tenantSlug, tenantID, userID, platform, token, appVersion string) error {
	schema := tenantschema.Name(tenantSlug)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("register device token: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	release := fmt.Sprintf(`DELETE FROM %s.user_device_tokens WHERE tenant_id = $1 AND token = $2 AND user_id <> $3`, schema)
	if _, err := tx.ExecContext(ctx, release, tenantID, token, userID); err != nil {
		return fmt.Errorf("register device token: %w", err)
	}
	upsert := fmt.Sprintf(`
		INSERT INTO %s.user_device_tokens (tenant_id, user_id, platform, token, app_version)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		ON CONFLICT (tenant_id, user_id, token) DO UPDATE
		SET last_seen_at = NOW(), app_version = EXCLUDED.app_version
	`, schema)
	if _, err := tx.ExecContext(ctx, upsert, tenantID, userID, platform, token, appVersion); err != nil {
		return fmt.Errorf("register device token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("register device token: %w", err)
	}
	return nil
}

// DeleteStaleDeviceTokens deletes the tenant's tokens last seen before
// cutoff and returns how many it deleted.
func (s *Store) DeleteStaleDeviceTokens(ctx context.Context, tenantSlug string, cutoff time.Time) (int64, error) {
	query := fmt.Sprintf(`DELETE FROM %s.user_device_tokens WHERE last_seen_at < $1`, tenantschema.Name(tenantSlug))

	res, err := s.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete stale device tokens: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete stale device tokens: %w", err)
	}
	return n, nil
}

// BootstrapDeviceTokens creates user_device_tokens and its user index in
// the given tenant's schema if they don't already exist, the same way
// BootstrapFeed does notifications.
func (s *Store) BootstrapDeviceTokens(ctx context.Context, tenantSlug string) error {
	schema := tenantschema.Name(tenantSlug)
	return s.bootstrap(ctx, "notifications.BootstrapDeviceTokens:"+tenantSlug, []string{
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.user_device_tokens (
			    id             UUID PRIMARY KEY DEFAULT uuidv7(),
			    tenant_id      UUID NOT NULL,
			    user_id        UUID NOT NULL,
			    platform       TEXT NOT NULL CHECK (platform IN ('ios', 'android', 'web')),
			    token          TEXT NOT NULL,
			    app_version    TEXT,
			    registered_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    UNIQUE (tenant_id, user_id, token)
			)
		`, schema),
		fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS idx_device_tokens_user
			    ON %s.user_device_tokens (tenant_id, user_id)
		`, schema),
		// RegisterDeviceToken finds a token's other users by it.
		fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS idx_device_tokens_token
			    ON %s.user_device_tokens (tenant_id, token)
		`, schema),
	})
}
