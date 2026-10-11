// Package notify sends user notifications through the engine's host.notify
// calls. A notification
// type is declared once with Define, and the Send, SendTx and SendBulk
// methods of the resulting Def send it to users, who get it in-app and on
// whichever other channels routing picks for them. The module needs the
// notify.send capability.
package notify

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// Channel identifies a notification delivery channel. Unsupported values are
// rejected by Define or when the notification is sent.
type Channel string

const (
	ChannelInApp Channel = "in_app"
	ChannelEmail Channel = "email"
	ChannelSMS   Channel = "sms"
	ChannelPush  Channel = "push"
)

// MaxBulkRecipients is the most users one Def.SendBulk may name; batch a
// larger audience through a background job (jobs.Enqueue).
const MaxBulkRecipients = abi.NotifyMaxBulkRecipients

// NotifyOption configures Def.Send, Def.SendTx and Def.SendBulk. An unset option leaves the
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
func ForceChannel(channel Channel) NotifyOption {
	return func(o *abi.NotifySendOptions) { o.ChannelOverride = string(channel) }
}

// AdditionalChannel adds channel on top of the type's defaults, unless the
// user turned it off.
func AdditionalChannel(channel Channel) NotifyOption {
	return func(o *abi.NotifySendOptions) {
		o.AdditionalChannels = append(o.AdditionalChannels, string(channel))
	}
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
