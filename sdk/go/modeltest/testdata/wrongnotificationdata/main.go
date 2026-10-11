package main

import (
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/notificationsfixture"
	"github.com/djangbahevans/goerp/sdk/go/notify"
)

func main() {
	var n *modeltest.TestNotifications
	_, _ = n.RenderTemplate(fixture.Confirmed, notify.ChannelEmail, "en", "wrong data")
}
