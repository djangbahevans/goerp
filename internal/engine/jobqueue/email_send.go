package jobqueue

import "github.com/riverqueue/river"

// NotificationDeliveryMaxAttempts is how many times a notification
// delivery job (email_send, sms_send, push_send) is tried before its
// delivery is left failed (notification-system.md §4).
const NotificationDeliveryMaxAttempts = 5

// EmailSendArgs is the email_send job the notification pipeline
// (internal/engine/notify) enqueues for each routed email delivery: one
// notification_deliveries row, identified by its notification and
// recipient address. Email is an engine-owned adapter rather than a
// provider category, so this is its own job kind, not a WASMJobArgs.
// Nothing works it yet: the email_send worker is goerp#1286.
type EmailSendArgs struct {
	TenantID       string `json:"tenant_id"`
	TenantSlug     string `json:"tenant_slug"`
	NotificationID string `json:"notification_id"`
	DeliveryID     string `json:"delivery_id"`
	Recipient      string `json:"recipient"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (EmailSendArgs) Kind() string { return "email_send" }

func (EmailSendArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEmail, MaxAttempts: NotificationDeliveryMaxAttempts}
}
