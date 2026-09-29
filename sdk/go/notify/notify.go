// Package notify is the module-side caller for the host.notify namespace
// (host-abi-reference.md §11, notification-system.md §7): Send, SendTx and
// SendBulk send one of the module's own declared notification_types to
// users, who get it in-app and on whichever other channels routing picks
// for them. The module needs the notify.send capability.
package notify

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
	"github.com/vmihailenco/msgpack/v5"
)

// Channels a notification can be delivered on.
const (
	ChannelInApp = "in_app"
	ChannelEmail = "email"
	ChannelSMS   = "sms"
	ChannelPush  = "push"
)

// MaxBulkRecipients is the most users one SendBulk may name; batch a
// larger audience through a background job (jobs.Enqueue).
const MaxBulkRecipients = abi.NotifyMaxBulkRecipients

// NotifyOption configures Send/SendTx/SendBulk. An unset option leaves the
// notification type's own defaults in place.
type NotifyOption func(*abi.NotifySendOptions)

// HighPriority sends the notification at high priority, overriding the
// type's default_priority.
func HighPriority() NotifyOption {
	return func(o *abi.NotifySendOptions) { o.Priority = "high" }
}

// NormalPriority sends the notification at normal priority, overriding
// the type's default_priority.
func NormalPriority() NotifyOption {
	return func(o *abi.NotifySendOptions) { o.Priority = "normal" }
}

// ForceChannel delivers on channel whatever the user's preferences. Use it
// sparingly: respecting preferences is the default. Only one channel can
// be forced; a later ForceChannel replaces an earlier one.
func ForceChannel(channel string) NotifyOption {
	return func(o *abi.NotifySendOptions) { o.ChannelOverride = channel }
}

// AdditionalChannel adds channel on top of the type's defaults, unless the
// user turned it off.
func AdditionalChannel(channel string) NotifyOption {
	return func(o *abi.NotifySendOptions) { o.AdditionalChannels = append(o.AdditionalChannels, channel) }
}

// WithIdempotencyKey makes the send happen at most once per recipient: a
// later send from this module to the same user with the same key creates
// nothing, so retrying a request cannot notify twice.
func WithIdempotencyKey(key string) NotifyOption {
	return func(o *abi.NotifySendOptions) { o.IdempotencyKey = key }
}

// WithActionURL replaces the in_app template's action_url: a browser path
// such as "/_m/sales/orders/{id}", not an API path.
func WithActionURL(url string) NotifyOption {
	return func(o *abi.NotifySendOptions) { o.ActionURL = url }
}

func buildOptions(opts []NotifyOption) abi.NotifySendOptions {
	var o abi.NotifySendOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Send sends notificationType ("{module}.{name}", one of this module's
// declared notification_types) to userID via host.notify.send, rendering
// its templates against data, which is msgpack-encoded and must encode as
// a map (a map or a struct). templateKey is usually notificationType; it
// selects nothing yet, since templates are resolved by type and channel.
// Delivery is asynchronous: Send returns once the notification and its
// delivery jobs are written.
func Send(userID, notificationType, templateKey string, data any, opts ...NotifyOption) error {
	encoded, err := msgpack.Marshal(data)
	if err != nil {
		return err
	}
	in := abi.NotifySendInput{
		UserID: userID, Type: notificationType, TemplateKey: templateKey, Data: encoded, Opts: buildOptions(opts),
	}
	return hostcall.Do(hostNotifySend, in, &abi.NotifySendOutput{})
}

// SendTx is Send inside tx via host.notify.send_tx: the notification and
// its delivery jobs exist only if tx commits, and the recipient's open
// sessions hear of it only then.
func SendTx(tx *db.Tx, userID, notificationType, templateKey string, data any, opts ...NotifyOption) error {
	encoded, err := msgpack.Marshal(data)
	if err != nil {
		return err
	}
	in := abi.NotifySendTxInput{
		TxID: tx.TxID(), UserID: userID, Type: notificationType, TemplateKey: templateKey, Data: encoded, Opts: buildOptions(opts),
	}
	return hostcall.Do(hostNotifySendTx, in, &abi.NotifySendOutput{})
}

// SendBulk is Send to each of userIDs, at most MaxBulkRecipients, with the
// same data for every one, via host.notify.send_bulk. Every notification
// is written together or none is: one user who isn't a member of the
// tenant fails the whole call. For per-recipient data, call Send for each
// user instead.
func SendBulk(userIDs []string, notificationType, templateKey string, data any, opts ...NotifyOption) error {
	encoded, err := msgpack.Marshal(data)
	if err != nil {
		return err
	}
	in := abi.NotifySendBulkInput{
		UserIDs: userIDs, Type: notificationType, TemplateKey: templateKey, Data: encoded, Opts: buildOptions(opts),
	}
	return hostcall.Do(hostNotifySendBulk, in, &abi.NotifySendBulkOutput{})
}
