package notify

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// RemoveDeviceToken removes token from the calling tenant, including all users
// registered with that token. A missing token succeeds. Requires notify.manage_deliveries.
func RemoveDeviceToken(token string) error {
	return hostcall.Do(hostNotifyRemoveDeviceToken, abi.NotifyRemoveDeviceTokenInput{Token: token}, nil)
}

// UpdateDeliveryStatus reports one SMS or push recipient's outcome in the calling
// tenant. Status is accepted, delivered, quota_exceeded or failed; reason may be empty.
// Requires notify.manage_deliveries. A missing delivery returns notify.delivery_not_found;
// an invalid channel, recipient or status returns notify.invalid_options.
// The correction commits independently of any open module transaction.
func UpdateDeliveryStatus(notificationID, channel, recipient, status, reason string) error {
	in := abi.NotifyUpdateDeliveryStatusInput{
		NotificationID: notificationID,
		Channel:        channel,
		Recipient:      recipient,
		Status:         status,
		Reason:         reason,
	}

	return hostcall.Do(hostNotifyUpdateDeliveryStatus, in, nil)
}
