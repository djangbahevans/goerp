package ws

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

func TestHub_FeedAdmissionAndDelivery(t *testing.T) {
	for _, lookupFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("lookupFails=%t", lookupFails), func(t *testing.T) {
			hub := NewHub()
			var disabled, denied atomic.Bool
			access := AccessChecks{
				ModuleEnabled: func(_ context.Context, mod string) (bool, error) {
					if disabled.Load() && lookupFails {
						return false, errors.New("entitlement lookup failed")
					}
					return mod == "contacts" && !disabled.Load(), nil
				},
				ReadResource: func(_ context.Context, mod, resource string) (bool, error) {
					if denied.Load() && lookupFails {
						return false, errors.New("membership lookup failed")
					}
					return mod == "contacts" && resource == "contacts.contact" && !denied.Load(), nil
				},
			}
			conn := dial(t, testServerWithAccess(t, hub, "tenant-1", access))
			for _, channel := range []string{"feed:unknown", "feed:", "feed:contacts:extra"} {
				subscribe(t, conn, channel)
				if env := readEnvelope(t, conn); env.Type != "subscription.denied" || env.Channel != channel {
					t.Fatalf("admission = %+v", env)
				}
			}
			const channel = "feed:contacts"
			subscribe(t, conn, channel)
			waitForSubscriber(t, hub, channel)

			broadcast := func(payload any, want int) {
				t.Helper()
				if n, err := hub.BroadcastTenant(t.Context(), channel, "tenant-1", "records.changed", payload); err != nil || n != want {
					t.Fatalf("payload %#v reached %d, want %d: %v", payload, n, want, err)
				}
			}
			for _, payload := range []any{
				map[string]string{"resource": "contacts.contact"},
				struct {
					Resource string `json:"resource"`
				}{Resource: "contacts.contact"},
				jsontext.Value(`{"resource":"contacts.contact"}`),
			} {
				broadcast(payload, 1)
				if env := readEnvelope(t, conn); env.Type != "records.changed" {
					t.Fatalf("feed delivery = %+v", env)
				}
			}
			for _, payload := range []any{nil, map[string]string{}, map[string]int{"resource": 1}, map[string]string{"resource": "contacts.private"}, map[string]string{"resource": "sales.order"}} {
				broadcast(payload, 0)
				waitForSubscriber(t, hub, channel)
			}
			denied.Store(true)
			broadcast(map[string]string{"resource": "contacts.contact"}, 0)
			waitForSubscriber(t, hub, channel)
			denied.Store(false)
			broadcast(map[string]string{"resource": "contacts.contact"}, 1)
			if env := readEnvelope(t, conn); env.Type != "records.changed" {
				t.Fatalf("restored permission delivery = %+v", env)
			}

			disabled.Store(true)
			broadcast(map[string]string{"resource": "contacts.contact"}, 0)
			waitForSubscriberCount(t, hub, channel, 0)
			subscribe(t, conn, channel)
			if env := readEnvelope(t, conn); env.Type != "subscription.denied" {
				t.Fatalf("disabled module admission = %+v", env)
			}
		})
	}
}

func TestHub_RecordDeliveryRevokesSubscription(t *testing.T) {
	for _, prefix := range []string{"record:", "ui:record:"} {
		for _, lookupFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/lookupFails=%t", prefix, lookupFails), func(t *testing.T) {
				hub := NewHub()
				var revoked atomic.Bool
				access := AccessChecks{ReadRecord: func(_ context.Context, resource, id string) (bool, error) {
					if revoked.Load() && lookupFails {
						return false, errors.New("record read failed")
					}
					return resource == "contacts.contact" && id == "1" && !revoked.Load(), nil
				}}
				conn := dial(t, testServerWithAccess(t, hub, "tenant-1", access))
				other := dial(t, testServerWithAccess(t, hub, "tenant-2", access))
				for _, target := range []string{"contacts.contact:2", "unknown:1", "contacts.contact:", ":1", "contacts.contact:1:extra"} {
					channel := prefix + target
					subscribe(t, conn, channel)
					if env := readEnvelope(t, conn); env.Type != "subscription.denied" || env.Channel != channel {
						t.Fatalf("record refusal = %+v", env)
					}
				}
				channel := prefix + "contacts.contact:1"
				subscribe(t, conn, channel)
				subscribe(t, other, channel)
				waitForSubscriberCount(t, hub, channel, 2)
				if n, err := hub.BroadcastTenant(t.Context(), channel, "tenant-1", "private", nil); err != nil || n != 1 {
					t.Fatalf("record broadcast reached %d: %v", n, err)
				}
				if env := readEnvelope(t, conn); env.Type != "private" {
					t.Fatalf("record delivery = %+v", env)
				}

				revoked.Store(true)
				if n, err := hub.BroadcastTenant(t.Context(), channel, "tenant-1", "private", nil); err != nil || n != 0 {
					t.Fatalf("revoked record broadcast reached %d: %v", n, err)
				}
				waitForSubscriberCount(t, hub, channel, 1)
				subscribe(t, conn, channel)
				if env := readEnvelope(t, conn); env.Type != "subscription.denied" {
					t.Fatalf("revoked admission = %+v", env)
				}
				revoked.Store(false)
				if n, err := hub.BroadcastTenant(t.Context(), channel, "tenant-1", "private", nil); err != nil || n != 0 {
					t.Fatalf("removed subscription reached %d: %v", n, err)
				}
			})
		}
	}
}
