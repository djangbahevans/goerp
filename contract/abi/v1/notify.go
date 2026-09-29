package abi

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
