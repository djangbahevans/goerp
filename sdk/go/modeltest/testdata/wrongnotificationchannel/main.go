package main

import (
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/notificationsfixture"
)

type unrelatedChannel string

func main() {
	var n *modeltest.TestNotifications
	channel := "email"
	unrelated := unrelatedChannel("email")

	_, _ = n.RenderTemplate(fixture.Confirmed, channel, "en", fixture.Data{})
	_, _ = n.RenderTemplate(fixture.Confirmed, unrelated, "en", fixture.Data{})
}
