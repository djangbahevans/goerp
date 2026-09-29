package abi

// NotifyMaxBulkRecipients is the most distinct user_ids one
// host.notify.send_bulk call may name.
const NotifyMaxBulkRecipients = 1000

// NotifySendOptions is the opts member of host.notify.send/send_tx/
// send_bulk (notification-system.md §7 "Notify options"). Zero values
// leave the notification type's own defaults in place.
type NotifySendOptions struct {
	// Priority is "normal" or "high", overriding the type's
	// default_priority.
	Priority string `msgpack:"priority,omitempty"`
	// ChannelOverride is delivered whatever the user's preferences
	// (notify.ForceChannel).
	ChannelOverride string `msgpack:"channel_override,omitempty"`
	// AdditionalChannels are delivered unless the user turned them off
	// (notify.AdditionalChannel).
	AdditionalChannels []string `msgpack:"additional_channels,omitempty"`
	// ActionURL replaces the in_app template's action_url.
	ActionURL string `msgpack:"action_url,omitempty"`
	// IdempotencyKey makes the send happen at most once per recipient: a
	// later send from the same module to the same user with the same key
	// creates nothing and reports the first one's notification.
	IdempotencyKey string `msgpack:"idempotency_key,omitempty"`
}

// NotifySendInput is the request of host.notify.send. Type is the full
// "{module}.{name}" notification type, which must be one of the calling
// module's own. Data is the msgpack-encoded template variables, a map.
// TemplateKey is accepted for the ABI's shape but selects nothing yet:
// templates are resolved by type and channel.
type NotifySendInput struct {
	UserID      string            `msgpack:"user_id"`
	Type        string            `msgpack:"type"`
	TemplateKey string            `msgpack:"template_key"`
	Data        []byte            `msgpack:"data"`
	Opts        NotifySendOptions `msgpack:"opts"`
}

// NotifySendTxInput is the request of host.notify.send_tx: the shape of
// NotifySendInput scoped to an open host.db transaction.
type NotifySendTxInput struct {
	TxID        string            `msgpack:"tx_id"`
	UserID      string            `msgpack:"user_id"`
	Type        string            `msgpack:"type"`
	TemplateKey string            `msgpack:"template_key"`
	Data        []byte            `msgpack:"data"`
	Opts        NotifySendOptions `msgpack:"opts"`
}

// NotifySendOutput is the response of host.notify.send/send_tx:
// ChannelsUsed is every channel a delivery was created for, in_app first.
// Deduplicated is true when the idempotency key matched an earlier
// notification, which is then the one reported.
type NotifySendOutput struct {
	NotificationID string   `msgpack:"notification_id"`
	ChannelsUsed   []string `msgpack:"channels_used"`
	Deduplicated   bool     `msgpack:"deduplicated"`
}

// NotifySendBulkInput is the request of host.notify.send_bulk: the same
// notification, with the same data, to at most 1000 users.
type NotifySendBulkInput struct {
	UserIDs     []string          `msgpack:"user_ids"`
	Type        string            `msgpack:"type"`
	TemplateKey string            `msgpack:"template_key"`
	Data        []byte            `msgpack:"data"`
	Opts        NotifySendOptions `msgpack:"opts"`
}

// NotifyRecipientResult is one recipient's outcome of host.notify.send_bulk.
type NotifyRecipientResult struct {
	UserID         string   `msgpack:"user_id"`
	NotificationID string   `msgpack:"notification_id"`
	ChannelsUsed   []string `msgpack:"channels_used"`
	Deduplicated   bool     `msgpack:"deduplicated"`
}

// NotifySendBulkOutput is the response of host.notify.send_bulk: one
// result per distinct user, in user_ids order.
type NotifySendBulkOutput struct {
	Notifications []NotifyRecipientResult `msgpack:"notifications"`
}

// Provider-category delivery job types the notification pipeline
// enqueues through host.jobs.enqueue_provider (connector-guide.md §8, §9).
// A provider connector handles them with engine.OnJob.
const (
	JobTypeSMSSend  = "sms_send"
	JobTypePushSend = "push_send"
)

// ProviderPayloadSchemaVersion is the schema_version of SMSSendPayload
// and PushSendPayload. A field added within a version is optional to
// read; a breaking change is a new job type, not a new version of this
// one.
const ProviderPayloadSchemaVersion = 1

// SMSSendPayload is an sms_send job's payload: one rendered SMS to one
// phone number. From is the tenant's sender ID, empty for the provider's
// default. IdempotencyKey is the delivery's provider-neutral key
// ("{notification_id}:sms:{to}"), for a provider with native idempotency.
type SMSSendPayload struct {
	SchemaVersion  int    `msgpack:"schema_version"`
	TenantID       string `msgpack:"tenant_id"`
	NotificationID string `msgpack:"notification_id"`
	To             string `msgpack:"to"`
	From           string `msgpack:"from"`
	Body           string `msgpack:"body"`
	IdempotencyKey string `msgpack:"idempotency_key"`
}

// PushSendPayload is a push_send job's payload: one rendered push
// notification to each of a user's device tokens. Each token is its own
// delivery, with its own idempotency key. ActionURL is the browser path
// the app opens on tap; Data carries the notification's ID and type for
// the app.
type PushSendPayload struct {
	SchemaVersion  int               `msgpack:"schema_version"`
	TenantID       string            `msgpack:"tenant_id"`
	NotificationID string            `msgpack:"notification_id"`
	Tokens         []PushDeviceToken `msgpack:"tokens"`
	Title          string            `msgpack:"title"`
	Body           string            `msgpack:"body"`
	ActionURL      string            `msgpack:"action_url"`
	Data           map[string]string `msgpack:"data"`
}

// PushDeviceToken is one device a push_send job delivers to. Platform is
// "ios", "android" or "web".
type PushDeviceToken struct {
	Platform       string `msgpack:"platform"`
	Token          string `msgpack:"token"`
	IdempotencyKey string `msgpack:"idempotency_key"`
}
