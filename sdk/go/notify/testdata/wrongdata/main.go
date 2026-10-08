// Command wrongdata must not compile: it sends a notification whose data is
// not the definition's data type. def_test.go builds it and expects the
// failure.
package main

import "github.com/djangbahevans/goerp/sdk/go/notify"

type orderConfirmed struct{ OrderReference string }

type orderShipped struct{ TrackingNumber string }

var confirmed = notify.Define[orderConfirmed]("order_confirmed", notify.Label("Order confirmed"))

func main() {
	_ = confirmed.Send("user-1", orderShipped{TrackingNumber: "TRK-1"})
}
