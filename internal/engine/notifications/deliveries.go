package notifications

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// DeliveriesTable is the per-recipient delivery tracking table's
// unqualified name.
const DeliveriesTable = "notification_deliveries"

// Delivery statuses (notification-system.md §3). A row starts pending,
// except in_app's, which is delivered on insert: the feed row is the
// delivery.
const (
	DeliveryPending       = "pending"
	DeliveryRetrying      = "retrying"
	DeliveryAccepted      = "accepted"
	DeliveryDelivered     = "delivered"
	DeliveryQuotaExceeded = "quota_exceeded"
	DeliveryFailed        = "failed"
	DeliverySkipped       = "skipped"
)

// NewNotification is one feed row to insert. Data is the raw template
// data, kept for re-rendering. IdempotencyKey, when set, makes the insert
// happen at most once per (user, module, key).
type NewNotification struct {
	TenantID  string
	UserID    string
	Type      string
	Module    string
	Title     string
	Body      *string
	ActionURL *string
	Icon      *string
	Data      map[string]any

	IdempotencyKey string
}

// NewDelivery is one notification_deliveries row to insert: one channel
// to one recipient (a user ID, email address, phone number or push device
// token). Provider is empty when there is none (in_app).
type NewDelivery struct {
	Channel   string
	Recipient string
	Provider  string
}

// Delivery is one inserted notification_deliveries row.
type Delivery struct {
	ID             string
	Channel        string
	Recipient      string
	Status         string
	IdempotencyKey string
}

// DeliveryIdempotencyKey is the provider-neutral key every send of one
// delivery carries (notification-system.md §3 "Provider-neutral
// idempotency contract").
func DeliveryIdempotencyKey(notificationID, channel, recipient string) string {
	return notificationID + ":" + channel + ":" + recipient
}

// CreateTx inserts n on tx and returns the stored row, and whether it
// was inserted. When n carries an IdempotencyKey its user and module
// already have a notification under, nothing is inserted and that
// notification is returned instead.
func CreateTx(ctx context.Context, tx *sql.Tx, tenantSlug string, n NewNotification) (*Notification, bool, error) {
	data, err := json.Marshal(n.Data)
	if err != nil {
		return nil, false, fmt.Errorf("encode notification data: %w", err)
	}
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		INSERT INTO %s.notifications (tenant_id, user_id, type, module, title, body, action_url, icon, data, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''))
		ON CONFLICT (tenant_id, user_id, module, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING %s
	`, schema, notificationColumns)

	row := tx.QueryRowContext(ctx, query, n.TenantID, n.UserID, n.Type, n.Module, n.Title, n.Body, n.ActionURL, n.Icon, data, n.IdempotencyKey)
	created, err := scanNotification(row)
	if err == nil {
		return created, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("create notification: %w", err)
	}

	existing, err := scanNotification(tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT %s FROM %s.notifications
		WHERE tenant_id = $1 AND user_id = $2 AND module = $3 AND idempotency_key = $4
	`, notificationColumns, schema), n.TenantID, n.UserID, n.Module, n.IdempotencyKey))
	if err != nil {
		return nil, false, fmt.Errorf("load notification with idempotency key %q: %w", n.IdempotencyKey, err)
	}
	return existing, false, nil
}

// DeliveryChannelsTx returns every channel notificationID has a delivery
// on, in_app first and the rest in email, sms, push order.
func DeliveryChannelsTx(ctx context.Context, tx *sql.Tx, tenantSlug, notificationID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT channel FROM %s.notification_deliveries
		WHERE notification_id = $1
		GROUP BY channel
		ORDER BY array_position(ARRAY['in_app', 'email', 'sms', 'push'], channel)
	`, tenantschema.Name(tenantSlug)), notificationID)
	if err != nil {
		return nil, fmt.Errorf("list notification delivery channels: %w", err)
	}
	defer rows.Close()

	var channels []string
	for rows.Next() {
		var ch string
		if err := rows.Scan(&ch); err != nil {
			return nil, fmt.Errorf("list notification delivery channels: %w", err)
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// CreateDeliveriesTx inserts one row per delivery for notificationID on
// tx, in order. An in_app row is inserted delivered; every other channel's
// is pending until its delivery job reports back.
func CreateDeliveriesTx(ctx context.Context, tx *sql.Tx, tenantSlug, tenantID, notificationID string, deliveries []NewDelivery) ([]Delivery, error) {
	query := fmt.Sprintf(`
		INSERT INTO %s.notification_deliveries
		    (notification_id, tenant_id, channel, recipient, status, provider, idempotency_key, attempted_at, delivered_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7,
		        CASE WHEN $5 = 'delivered' THEN NOW() END,
		        CASE WHEN $5 = 'delivered' THEN NOW() END)
		RETURNING id
	`, tenantschema.Name(tenantSlug))

	out := make([]Delivery, 0, len(deliveries))
	for _, d := range deliveries {
		status := DeliveryPending
		if d.Channel == ChannelInApp {
			status = DeliveryDelivered
		}
		key := DeliveryIdempotencyKey(notificationID, d.Channel, d.Recipient)
		var id string
		if err := tx.QueryRowContext(ctx, query, notificationID, tenantID, d.Channel, d.Recipient, status, d.Provider, key).Scan(&id); err != nil {
			return nil, fmt.Errorf("create %s notification delivery: %w", d.Channel, err)
		}
		out = append(out, Delivery{ID: id, Channel: d.Channel, Recipient: d.Recipient, Status: status, IdempotencyKey: key})
	}
	return out, nil
}

// BootstrapDeliveries creates notification_deliveries and its indexes in
// the given tenant's schema if they don't already exist, the same way
// BootstrapFeed does notifications, which it references — so it runs
// after BootstrapFeed.
func (s *Store) BootstrapDeliveries(ctx context.Context, tenantSlug string) error {
	schema := tenantschema.Name(tenantSlug)
	// The UNIQUE constraint's index leads with notification_id, so it also
	// serves the per-notification lookup §3's idx_deliveries_notification is
	// for.
	return s.bootstrap(ctx, "notifications.BootstrapDeliveries:"+tenantSlug, []string{
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.notification_deliveries (
			    id               UUID PRIMARY KEY DEFAULT uuidv7(),
			    notification_id  UUID NOT NULL REFERENCES %s.notifications(id) ON DELETE CASCADE,
			    tenant_id        UUID NOT NULL,
			    channel          TEXT NOT NULL CHECK (channel IN ('in_app', 'email', 'sms', 'push')),
			    recipient        TEXT NOT NULL,
			    status           TEXT NOT NULL DEFAULT 'pending'
			                         CHECK (status IN ('pending', 'retrying', 'accepted', 'delivered', 'quota_exceeded', 'failed', 'skipped')),
			    provider         TEXT,
			    provider_id      TEXT,
			    idempotency_key  TEXT NOT NULL,
			    attempted_at     TIMESTAMPTZ,
			    delivered_at     TIMESTAMPTZ,
			    failure_reason   TEXT,
			    attempt_count    INT NOT NULL DEFAULT 0,
			    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    UNIQUE (notification_id, channel, recipient)
			)
		`, schema, schema),
		fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS idx_deliveries_pending
			    ON %s.notification_deliveries (tenant_id, status, created_at)
			    WHERE status IN ('pending', 'retrying')
		`, schema),
	})
}
