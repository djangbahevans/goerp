package notifications

import (
	"context"
	"errors"
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

var ErrDeliveryNotFound = errors.New("notification delivery not found")

func (s *Store) RemoveDeviceToken(ctx context.Context, tenantSlug, tenantID, token string) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		DELETE FROM %s.user_device_tokens WHERE tenant_id = $1 AND token = $2
	`, tenantschema.Name(tenantSlug)), tenantID, token)
	if err != nil {
		return fmt.Errorf("remove device token: %w", err)
	}

	return nil
}

func (s *Store) UpdateDeliveryStatus(ctx context.Context, tenantSlug, tenantID string, in abiv1.NotifyUpdateDeliveryStatusInput) error {
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s.notification_deliveries SET
			status = $5,
			failure_reason = NULLIF($6, ''),
			delivered_at = CASE WHEN $5 = 'delivered' THEN COALESCE(delivered_at, NOW()) END
		WHERE tenant_id = $1 AND notification_id = $2 AND channel = $3 AND recipient = $4
	`, tenantschema.Name(tenantSlug)), tenantID, in.NotificationID, in.Channel, in.Recipient, in.Status, in.Reason)
	if err != nil {
		return fmt.Errorf("update notification delivery status: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated notification deliveries: %w", err)
	}
	if n == 0 {
		return ErrDeliveryNotFound
	}

	return nil
}
