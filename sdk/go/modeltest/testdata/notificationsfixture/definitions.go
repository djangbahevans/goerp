// Package notificationsfixture declares typed notifications for harness integration tests.
package notificationsfixture

import "github.com/djangbahevans/goerp/sdk/go/notify"

type Data struct {
	Reference string `json:"reference"`
	Customer  string
	Amount    int64
	Items     []string
	ActionURL string
}

var Confirmed = notify.Define[Data]("confirmed",
	notify.Label("Order confirmed"),
	notify.DefaultChannels(notify.ChannelInApp, notify.ChannelEmail),
	notify.AvailableChannels(notify.ChannelInApp, notify.ChannelEmail, notify.ChannelSMS, notify.ChannelPush),
	notify.Template(notify.ChannelInApp, "notifications/in_app.{locale}.json"),
	notify.Template(notify.ChannelEmail, "notifications/email.{locale}.html"),
	notify.Template(notify.ChannelSMS, "notifications/sms.{locale}.txt"),
	notify.Template(notify.ChannelPush, "notifications/push.{locale}.json"),
)

var Other = notify.Define[Data]("other", notify.Label("Other notification"),
	notify.Template(notify.ChannelInApp, "notifications/in_app.{locale}.json"),
)
