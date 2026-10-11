package main

import "github.com/djangbahevans/goerp/sdk/go/notify"

type unrelatedChannel string

func main() {
	channel := "email"
	unrelated := unrelatedChannel("email")

	_ = notify.DefaultChannels(channel)
	_ = notify.AvailableChannels(channel)
	_ = notify.Template(channel, "email.{locale}.html")
	_ = notify.ForceChannel(channel)
	_ = notify.AdditionalChannel(channel)

	_ = notify.DefaultChannels(unrelated)
	_ = notify.AvailableChannels(unrelated)
	_ = notify.Template(unrelated, "email.{locale}.html")
	_ = notify.ForceChannel(unrelated)
	_ = notify.AdditionalChannel(unrelated)
}
