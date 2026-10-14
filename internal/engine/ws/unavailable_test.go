package ws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
)

func TestHub_RecordUnavailable_TerminalDelivery(t *testing.T) {
	for _, prefix := range []string{"record:", "ui:record:"} {
		for _, crossInstance := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/redis=%t", prefix, crossInstance), func(t *testing.T) {
				publisher := NewHub()
				receiver := publisher
				if crossInstance {
					publisher, _ = redisHub(t)
					receiver, _ = redisHub(t)
				}
				var missing atomic.Bool
				access := AccessChecks{
					ReadRecord:     func(context.Context, string, string) (bool, error) { return !missing.Load(), nil },
					RecordEligible: func(context.Context, string) (bool, error) { return true, nil },
				}
				conn := dial(t, testServerWithAccess(t, receiver, "tenant-1", access))
				other := dial(t, testServerWithAccess(t, receiver, "tenant-2", access))
				channel := prefix + "contacts.contact:1"
				subscribe(t, conn, channel)
				subscribe(t, other, channel)
				subscribe(t, conn, TenantChannel("tenant-1"))
				waitForSubscriberCount(t, receiver, channel, 2)
				waitForSubscriber(t, receiver, TenantChannel("tenant-1"))
				missing.Store(true)
				if _, err := publisher.BroadcastTenant(t.Context(), channel, "tenant-1", "record.deleted", map[string]string{"secret": "private"}); err != nil {
					t.Fatal(err)
				}
				env := readEnvelope(t, conn)
				payload, ok := env.Payload.(map[string]any)
				if !ok || env.Type != "record.unavailable" || env.Channel != channel || !maps.Equal(payload, map[string]any{"resource": "contacts.contact", "record_id": "1"}) {
					t.Fatalf("terminal envelope = %+v", env)
				}
				waitForSubscriberCount(t, receiver, channel, 1)
				if _, err := publisher.BroadcastTenant(t.Context(), channel, "tenant-1", "record.updated", map[string]string{"secret": "private"}); err != nil {
					t.Fatal(err)
				}
				if _, err := publisher.BroadcastTenant(t.Context(), TenantChannel("tenant-1"), "tenant-1", "barrier", nil); err != nil {
					t.Fatal(err)
				}
				if env := readEnvelope(t, conn); env.Type != "barrier" {
					t.Fatalf("removed subscription received %+v", env)
				}
				subscribe(t, conn, channel)
				if env := readEnvelope(t, conn); env.Type != "subscription.denied" {
					t.Fatalf("missing record admission = %+v", env)
				}
				if _, err := publisher.BroadcastTenant(t.Context(), channel, "tenant-2", "record.updated", nil); err != nil {
					t.Fatal(err)
				}
				if env := readEnvelope(t, other); env.Type != "record.unavailable" {
					t.Fatalf("other tenant terminal = %+v", env)
				}
			})
		}
	}
}

func TestHub_RecordUnavailable_FailsClosed(t *testing.T) {
	for _, outcome := range []string{"read_error", "ineligible", "eligibility_error"} {
		t.Run(outcome, func(t *testing.T) {
			hub := NewHub()
			var revoked atomic.Bool
			var eligibilityCalls atomic.Int64
			access := AccessChecks{
				ReadRecord: func(context.Context, string, string) (bool, error) {
					if !revoked.Load() {
						return true, nil
					}
					if outcome == "read_error" {
						return false, errors.New("read failed")
					}
					return false, nil
				},
				RecordEligible: func(context.Context, string) (bool, error) {
					eligibilityCalls.Add(1)
					if outcome == "eligibility_error" {
						return false, errors.New("eligibility failed")
					}
					return false, nil
				},
			}
			conn := dial(t, testServerWithAccess(t, hub, "tenant-1", access))
			const channel = "record:contacts.contact:1"
			subscribe(t, conn, channel)
			subscribe(t, conn, TenantChannel("tenant-1"))
			waitForSubscriber(t, hub, channel)
			waitForSubscriber(t, hub, TenantChannel("tenant-1"))
			revoked.Store(true)
			if n, err := hub.BroadcastTenant(t.Context(), channel, "tenant-1", "record.updated", map[string]string{"secret": "private"}); err != nil || n != 0 {
				t.Fatalf("failed check reached %d: %v", n, err)
			}
			waitForSubscriberCount(t, hub, channel, 0)
			if outcome == "read_error" && eligibilityCalls.Load() != 0 {
				t.Fatal("read failure was treated as confirmed unavailability")
			}
			if _, err := hub.BroadcastTenant(t.Context(), TenantChannel("tenant-1"), "tenant-1", "barrier", nil); err != nil {
				t.Fatal(err)
			}
			if env := readEnvelope(t, conn); env.Type != "barrier" {
				t.Fatalf("failed check leaked %+v", env)
			}
		})
	}
}

func TestHub_RecordUnavailable_ConcurrentBroadcastsNotifyOnce(t *testing.T) {
	hub := NewHub()
	var revoked atomic.Bool
	access := AccessChecks{
		ReadRecord:     func(context.Context, string, string) (bool, error) { return !revoked.Load(), nil },
		RecordEligible: func(context.Context, string) (bool, error) { return true, nil },
	}
	conn := dial(t, testServerWithAccess(t, hub, "tenant-1", access))
	const channel = "record:contacts.contact:1"
	subscribe(t, conn, channel)
	subscribe(t, conn, TenantChannel("tenant-1"))
	waitForSubscriber(t, hub, channel)
	waitForSubscriber(t, hub, TenantChannel("tenant-1"))
	revoked.Store(true)
	var wg sync.WaitGroup
	var reached atomic.Int64
	for range 20 {
		wg.Go(func() {
			n, err := hub.BroadcastTenant(t.Context(), channel, "tenant-1", "record.updated", nil)
			if err != nil {
				t.Error(err)
			}
			reached.Add(int64(n))
		})
	}
	wg.Wait()
	if reached.Load() != 1 {
		t.Fatalf("terminal messages = %d, want 1", reached.Load())
	}
	if env := readEnvelope(t, conn); env.Type != "record.unavailable" {
		t.Fatal(env)
	}
	if _, err := hub.BroadcastTenant(t.Context(), TenantChannel("tenant-1"), "tenant-1", "barrier", nil); err != nil {
		t.Fatal(err)
	}
	if env := readEnvelope(t, conn); env.Type != "barrier" {
		t.Fatalf("duplicate terminal %+v", env)
	}
}

func TestHub_RecordAliases_CountConnectionsAndNotifyEachSubscription(t *testing.T) {
	hub := NewHub()
	var revoked atomic.Bool
	access := AccessChecks{
		ReadRecord:     func(context.Context, string, string) (bool, error) { return !revoked.Load(), nil },
		RecordEligible: func(context.Context, string) (bool, error) { return true, nil },
	}
	conn := dial(t, testServerWithAccess(t, hub, "tenant-1", access))
	channels := []string{"record:contacts.contact:1", "ui:record:contacts.contact:1"}
	for _, channel := range channels {
		subscribe(t, conn, channel)
		waitForSubscriber(t, hub, channel)
	}

	for _, message := range []string{"record.updated", "record.unavailable"} {
		revoked.Store(message == "record.unavailable")
		if n, err := hub.BroadcastTenant(t.Context(), channels[0], "tenant-1", "record.updated", nil); err != nil || n != 1 {
			t.Fatalf("broadcast reached %d connections: %v", n, err)
		}
		seen := make(map[string]bool)
		for range channels {
			env := readEnvelope(t, conn)
			if env.Type != message || seen[env.Channel] {
				t.Fatalf("alias delivery = %+v, seen %v", env, seen)
			}
			seen[env.Channel] = true
		}
		for _, channel := range channels {
			if !seen[channel] {
				t.Fatalf("missing delivery for %s", channel)
			}
		}
	}

	if n, err := hub.BroadcastTenant(t.Context(), channels[0], "tenant-1", "record.updated", nil); err != nil || n != 0 {
		t.Fatalf("removed subscriptions reached %d connections: %v", n, err)
	}
}
