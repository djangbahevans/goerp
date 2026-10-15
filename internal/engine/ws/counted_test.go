package ws

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/coder/websocket"
	"github.com/djangbahevans/goerp/internal/engine/cache"
)

type isolatedCountedBus struct {
	*cache.Client
	prefix string
}

func (b *isolatedCountedBus) Publish(ctx context.Context, channel, payload string) error {
	return b.Client.Publish(ctx, b.prefix+channel, payload)
}

func (b *isolatedCountedBus) PublishCount(ctx context.Context, channel, payload string) (int, error) {
	return b.Client.PublishCount(ctx, b.prefix+channel, payload)
}

func (b *isolatedCountedBus) Subscribe(ctx context.Context, channel string) (<-chan cache.Message, func() error, error) {
	return b.Client.Subscribe(ctx, b.prefix+channel)
}

func countedRedisHub(t *testing.T, prefix string) (*Hub, *cache.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", MaxRetries: -1})
	if err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	hub := NewHub()
	hub.Start(t.Context(), &isolatedCountedBus{Client: client, prefix: prefix})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hub.Close(ctx)
	})
	return hub, client
}

func TestHub_CountedUserDelivery(t *testing.T) {
	for _, redis := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%t", redis), func(t *testing.T) {
			publisher := NewHub()
			var receiver *Hub
			if redis {
				prefix := "test:" + uuid.New().String() + ":"
				publisher, _ = countedRedisHub(t, prefix)
				receiver, _ = countedRedisHub(t, prefix)
			} else {
				receiver = publisher
			}
			tenant := uuid.New().String()
			channel := UserChannel("user-1")
			push := func(want int) {
				t.Helper()
				count, err := publisher.BroadcastCounted(t.Context(), channel, tenant, "ui.push", map[string]any{"instructions": []map[string]any{{"type": "reload"}}})
				if err != nil || count != want {
					t.Fatalf("counted delivery = %d, %v; want %d", count, err, want)
				}
			}
			push(0)
			first := dial(t, testServerAs(t, publisher, tenant, "user-1"))
			subscribe(t, first, channel)
			waitForSubscriber(t, publisher, channel)
			push(1)
			if env := readEnvelope(t, first); env.Type != "ui.push" || env.Channel != channel {
				t.Fatalf("first delivery = %+v", env)
			}

			second := dial(t, testServerAs(t, receiver, tenant, "user-1"))
			otherTenant := uuid.New().String()
			other := dial(t, testServerAs(t, receiver, otherTenant, "user-1"))
			wrongUser := dial(t, testServerAs(t, receiver, tenant, "user-2"))
			for _, conn := range []*websocket.Conn{second, other, wrongUser} {
				subscribe(t, conn, channel)
			}
			if env := readEnvelope(t, wrongUser); env.Type != "subscription.denied" {
				t.Fatalf("wrong user subscription = %+v", env)
			}
			wantSubs := 2
			if receiver == publisher {
				wantSubs++
			}
			waitForSubscriberCount(t, receiver, channel, wantSubs)
			push(2)
			for _, conn := range []*websocket.Conn{first, second} {
				if env := readEnvelope(t, conn); env.Type != "ui.push" {
					t.Fatalf("delivery = %+v", env)
				}
			}
			if count, err := publisher.BroadcastCounted(t.Context(), channel, otherTenant, "barrier", nil); err != nil || count != 1 {
				t.Fatalf("other tenant count = %d, %v", count, err)
			}
			if env := readEnvelope(t, other); env.Type != "barrier" {
				t.Fatalf("cross-tenant leak = %+v", env)
			}
			if _, err := publisher.BroadcastCounted(t.Context(), channel, tenant, "barrier", nil); err != nil {
				t.Fatal(err)
			}
			for _, conn := range []*websocket.Conn{first, second} {
				if env := readEnvelope(t, conn); env.Type != "barrier" {
					t.Fatalf("duplicate message = %+v", env)
				}
			}
			ownChannel := UserChannel("user-2")
			subscribe(t, wrongUser, ownChannel)
			waitForSubscriber(t, receiver, ownChannel)
			if _, err := publisher.BroadcastCounted(t.Context(), ownChannel, tenant, "barrier", nil); err != nil {
				t.Fatal(err)
			}
			if env := readEnvelope(t, wrongUser); env.Type != "barrier" {
				t.Fatalf("unauthorized message = %+v", env)
			}
		})
	}
}

func TestHub_CountedRevalidatesRoleAndRecordAliases(t *testing.T) {
	for _, redis := range []bool{false, true} {
		for _, channel := range []string{"ui:role:admin", "ui:record:sales.invoice:inv1"} {
			t.Run(fmt.Sprintf("%s/redis=%t", channel, redis), func(t *testing.T) {
				publisher := NewHub()
				var receiver *Hub
				if redis {
					prefix := "test:" + uuid.New().String() + ":"
					publisher, _ = countedRedisHub(t, prefix)
					receiver, _ = countedRedisHub(t, prefix)
				} else {
					receiver = publisher
				}
				tenant := uuid.New().String()
				var revoked atomic.Bool
				access := AccessChecks{
					Role:           func(context.Context, string) (bool, error) { return !revoked.Load(), nil },
					ReadRecord:     func(context.Context, string, string) (bool, error) { return !revoked.Load(), nil },
					RecordEligible: func(context.Context, string) (bool, error) { return true, nil },
				}
				conn := dial(t, testServerWithAccess(t, receiver, tenant, access))
				subscribe(t, conn, channel)
				waitForSubscriber(t, receiver, channel)
				record := channel != "ui:role:admin"
				if record {
					subscribe(t, conn, "record:sales.invoice:inv1")
					waitForSubscriber(t, receiver, "record:sales.invoice:inv1")
				}
				count, err := publisher.BroadcastCounted(t.Context(), channel, tenant, "ui.push", nil)
				if count != 1 || err != nil {
					t.Fatalf("authorized count = %d, %v", count, err)
				}
				messages := 1
				if record {
					messages++
				}
				seen := map[string]bool{}
				for range messages {
					env := readEnvelope(t, conn)
					if env.Type != "ui.push" || seen[env.Channel] {
						t.Fatalf("unexpected alias delivery = %+v", env)
					}
					seen[env.Channel] = true
				}
				revoked.Store(true)
				count, err = publisher.BroadcastCounted(t.Context(), channel, tenant, "ui.push", nil)
				if count != 0 || err != nil {
					t.Fatalf("revoked count = %d, %v", count, err)
				}
				if record {
					for range messages {
						if env := readEnvelope(t, conn); env.Type != "record.unavailable" {
							t.Fatalf("revoked record = %+v", env)
						}
					}
				}
				waitForSubscriberCount(t, receiver, channel, 0)
			})
		}
	}
}

func TestHub_CountedPublishFailureFallsBackLocally(t *testing.T) {
	hub, client := countedRedisHub(t, "test:"+uuid.New().String()+":")
	tenant := uuid.New().String()
	channel := UserChannel("user-1")
	conn := dial(t, testServerAs(t, hub, tenant, "user-1"))
	subscribe(t, conn, channel)
	waitForSubscriber(t, hub, channel)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if count, err := hub.BroadcastCounted(t.Context(), channel, tenant, "ui.push", nil); err != nil || count != 1 {
		t.Fatalf("fallback count = %d, %v", count, err)
	}
	if env := readEnvelope(t, conn); env.Type != "ui.push" {
		t.Fatalf("fallback delivery = %+v", env)
	}
}

type missingReceiptBus struct {
	countedBroadcastBus
}

func (b missingReceiptBus) PublishCount(ctx context.Context, channel, payload string) (int, error) {
	count, err := b.countedBroadcastBus.PublishCount(ctx, channel, payload)
	return count + 1, err
}

func TestHub_CountedMissingReceiptDoesNotReplay(t *testing.T) {
	hub, _ := countedRedisHub(t, "test:"+uuid.New().String()+":")
	tenant := uuid.New().String()
	channel := UserChannel("user-1")
	conn := dial(t, testServerAs(t, hub, tenant, "user-1"))
	subscribe(t, conn, channel)
	waitForSubscriber(t, hub, channel)
	publisher := NewHub()
	hub.mu.RLock()
	publisher.bus = missingReceiptBus{countedBroadcastBus: hub.bus.(countedBroadcastBus)}
	hub.mu.RUnlock()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	count, err := publisher.BroadcastCounted(ctx, channel, tenant, "ui.push", nil)
	if err == nil || count != 1 {
		t.Fatalf("missing receipt = %d, %v; want partial count and error", count, err)
	}
	if env := readEnvelope(t, conn); env.Type != "ui.push" {
		t.Fatalf("delivery = %+v", env)
	}
	if _, err := hub.broadcast(t.Context(), channel, "barrier", nil, func(c *Conn) bool { return c.TenantID == tenant }); err != nil {
		t.Fatal(err)
	}
	if env := readEnvelope(t, conn); env.Type != "barrier" {
		t.Fatalf("push replayed after missing receipt: %+v", env)
	}
}

type failedPublicationBus struct {
	countedBroadcastBus
}

func (b failedPublicationBus) PublishCount(context.Context, string, string) (int, error) {
	return 0, fmt.Errorf("Redis publication unavailable")
}

func TestHub_CountedPublicationFailureFallsBackLocally(t *testing.T) {
	receiver, _ := countedRedisHub(t, "test:"+uuid.New().String()+":")
	publisher := NewHub()
	receiver.mu.RLock()
	publisher.bus = failedPublicationBus{countedBroadcastBus: receiver.bus.(countedBroadcastBus)}
	receiver.mu.RUnlock()
	tenant := uuid.New().String()
	channel := UserChannel("user-1")
	conn := dial(t, testServerAs(t, publisher, tenant, "user-1"))
	subscribe(t, conn, channel)
	waitForSubscriber(t, publisher, channel)

	count, err := publisher.BroadcastCounted(t.Context(), channel, tenant, "ui.push", nil)
	if err != nil || count != 1 {
		t.Fatalf("publication failure fallback = %d, %v", count, err)
	}
	if env := readEnvelope(t, conn); env.Type != "ui.push" {
		t.Fatalf("fallback delivery = %+v", env)
	}
}
