package abi

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestNotifyDeliveryCorrectionWireFields(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input any
		keys  []string
	}{
		{
			name:  "remove token",
			input: NotifyRemoveDeviceTokenInput{Token: "token-1"},
			keys:  []string{"token"},
		},
		{
			name: "report with reason",
			input: NotifyUpdateDeliveryStatusInput{
				NotificationID: "n-1",
				Channel:        "push",
				Recipient:      "token-1",
				Status:         "failed",
				Reason:         "invalid token",
			},
			keys: []string{"notification_id", "channel", "recipient", "status", "reason"},
		},
		{
			name: "report without reason",
			input: NotifyUpdateDeliveryStatusInput{
				NotificationID: "n-1",
				Channel:        "sms",
				Recipient:      "+233501234567",
				Status:         "delivered",
			},
			keys: []string{"notification_id", "channel", "recipient", "status"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := msgpack.Marshal(tc.input)
			if err != nil {
				t.Fatal(err)
			}

			requireKeys(t, decodeMap(t, encoded), tc.keys...)
		})
	}
}
