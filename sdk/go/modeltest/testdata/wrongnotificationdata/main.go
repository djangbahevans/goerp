package main

import (
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/notificationsfixture"
)

func main() {
	var n *modeltest.TestNotifications
	_, _ = n.RenderTemplate(fixture.Confirmed, "email", "en", "wrong data")
}
