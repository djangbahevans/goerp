package ws

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/coder/websocket"
	"github.com/djangbahevans/goerp/internal/engine/cache"
)

func redisHub(t *testing.T) (*Hub, *cache.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	client, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", MaxRetries: -1})
	if err != nil {
		t.Skipf("Redis is unavailable (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	hub := NewHub()
	hub.Start(t.Context(), client)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hub.Close(ctx)
		select {
		case <-hub.done:
		default:
			t.Error("WebSocket broadcast listener did not stop")
		}
	})
	return hub, client
}

func TestHub_RedisBroadcastReachesBothInstancesOnce(t *testing.T) {
	publisher, _ := redisHub(t)
	receiver, _ := redisHub(t)
	tenant := uuid.New().String()
	channel := TenantChannel(tenant)
	conns := []*websocket.Conn{
		dial(t, testServerAs(t, publisher, tenant, "user-1")),
		dial(t, testServerAs(t, receiver, tenant, "user-1")),
	}
	for _, conn := range conns {
		subscribe(t, conn, channel)
	}
	waitForSubscriber(t, publisher, channel)
	waitForSubscriber(t, receiver, channel)

	for _, msgType := range []string{"schema.updated", "module.installed", "plan.changed"} {
		if n, err := publisher.BroadcastTenant(t.Context(), channel, tenant, msgType, map[string]string{"version": "v2"}); err != nil || n != 0 {
			t.Fatalf("Redis publish returned %d, %v; want asynchronous success", n, err)
		}
		for i, conn := range conns {
			env := readEnvelope(t, conn)
			if env.Channel != channel || env.Type != msgType || env.Payload.(map[string]any)["version"] != "v2" {
				t.Fatalf("instance %d received %+v, want %s", i, env, msgType)
			}
		}
	}
}

func TestHub_RedisUserBroadcastFiltersTenantAndUser(t *testing.T) {
	publisher, _ := redisHub(t)
	receiver, _ := redisHub(t)
	tenant := uuid.New().String()
	identities := []struct {
		tenant string
		user   string
	}{
		{tenant: tenant, user: "user-1"},
		{tenant: uuid.New().String(), user: "user-1"},
		{tenant: tenant, user: "user-2"},
	}
	var conns []*websocket.Conn
	for _, identity := range identities {
		conn := dial(t, testServerAs(t, receiver, identity.tenant, identity.user))
		subscribe(t, conn, NotificationsChannel)
		conns = append(conns, conn)
	}
	waitForSubscriberCount(t, receiver, NotificationsChannel, len(conns))

	if _, err := publisher.BroadcastUser(t.Context(), NotificationsChannel, tenant, "user-1", "notification.new", nil); err != nil {
		t.Fatal(err)
	}
	if env := readEnvelope(t, conns[0]); env.Type != "notification.new" {
		t.Fatalf("target received %+v", env)
	}
	for i, identity := range identities {
		if _, err := publisher.BroadcastUser(t.Context(), NotificationsChannel, identity.tenant, identity.user, "barrier", nil); err != nil {
			t.Fatal(err)
		}
		if env := readEnvelope(t, conns[i]); env.Type != "barrier" {
			t.Fatalf("identity %+v received unauthorized or duplicate message: %+v", identity, env)
		}
	}
}

func TestHub_RedisDeliveryRechecksAccess(t *testing.T) {
	for _, channel := range []string{"ui:role:admin", "feed:contacts", "record:contacts.contact:1", "ui:record:contacts.contact:1"} {
		for _, lookupFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/lookupFails=%t", channel, lookupFails), func(t *testing.T) {
				publisher, _ := redisHub(t)
				receiver, _ := redisHub(t)
				tenant := uuid.New().String()
				otherTenant := uuid.New().String()
				var revoked atomic.Bool
				allowed := func() (bool, error) {
					if revoked.Load() && lookupFails {
						return false, errors.New("access store unavailable")
					}
					return !revoked.Load(), nil
				}
				access := AccessChecks{
					Role: func(context.Context, string) (bool, error) { return allowed() },
					ModuleEnabled: func(context.Context, string) (bool, error) {
						return true, nil
					},
					ReadResource: func(context.Context, string, string) (bool, error) { return allowed() },
					ReadRecord:   func(context.Context, string, string) (bool, error) { return allowed() },
				}
				conn := dial(t, testServerWithAccess(t, receiver, tenant, access))
				other := dial(t, testServerWithAccess(t, receiver, otherTenant, access))
				for _, c := range []*websocket.Conn{conn, other} {
					subscribe(t, c, channel)
					subscribe(t, c, NotificationsChannel)
				}
				waitForSubscriberCount(t, receiver, channel, 2)
				waitForSubscriberCount(t, receiver, NotificationsChannel, 2)

				payload := map[string]string{"resource": "contacts.contact"}
				if _, err := publisher.BroadcastTenant(t.Context(), channel, tenant, "private", payload); err != nil {
					t.Fatal(err)
				}
				if env := readEnvelope(t, conn); env.Type != "private" {
					t.Fatalf("authorized delivery = %+v", env)
				}
				revoked.Store(true)
				if _, err := publisher.BroadcastTenant(t.Context(), channel, tenant, "revoked", payload); err != nil {
					t.Fatal(err)
				}
				if _, err := publisher.BroadcastUser(t.Context(), NotificationsChannel, tenant, "user-1", "barrier", nil); err != nil {
					t.Fatal(err)
				}
				if env := readEnvelope(t, conn); env.Type != "barrier" {
					t.Fatalf("revoked subscription received %+v", env)
				}
				if _, err := publisher.BroadcastUser(t.Context(), NotificationsChannel, otherTenant, "user-1", "barrier", nil); err != nil {
					t.Fatal(err)
				}
				if env := readEnvelope(t, other); env.Type != "barrier" {
					t.Fatalf("other tenant received %+v", env)
				}
				if _, feed := feedModule(channel); !feed {
					waitForSubscriberCount(t, receiver, channel, 1)
				}
			})
		}
	}
}

func TestHub_RedisPublishFailureFallsBackLocally(t *testing.T) {
	hub, client := redisHub(t)
	tenant := uuid.New().String()
	channel := TenantChannel(tenant)
	conn := dial(t, testServerAs(t, hub, tenant, "user-1"))
	subscribe(t, conn, channel)
	waitForSubscriber(t, hub, channel)

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	for _, msgType := range []string{"schema.updated", "barrier"} {
		if n, err := hub.BroadcastTenant(t.Context(), channel, tenant, msgType, nil); err != nil || n != 1 {
			t.Fatalf("local fallback reached %d: %v", n, err)
		}
		if env := readEnvelope(t, conn); env.Type != msgType {
			t.Fatalf("local fallback = %+v, want %s", env, msgType)
		}
	}
}

type retryBroadcastBus struct {
	*cache.Client
	attempts atomic.Int64
	ready    chan struct{}
}

func (b *retryBroadcastBus) Subscribe(ctx context.Context, channel string) (<-chan cache.Message, func() error, error) {
	if b.attempts.Add(1) == 1 {
		return nil, nil, errors.New("Redis temporarily unavailable")
	}

	msgs, closeFn, err := b.Client.Subscribe(ctx, channel)
	if err == nil {
		close(b.ready)
	}
	return msgs, closeFn, err
}

func TestHub_RedisSubscriptionRecoversFromInitialFailure(t *testing.T) {
	_, client := redisHub(t)
	bus := &retryBroadcastBus{Client: client, ready: make(chan struct{})}
	hub := NewHub()
	hub.Start(t.Context(), bus)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hub.Close(ctx)
	})

	tenant := uuid.New().String()
	channel := TenantChannel(tenant)
	conn := dial(t, testServerAs(t, hub, tenant, "user-1"))
	subscribe(t, conn, channel)
	waitForSubscriber(t, hub, channel)
	select {
	case <-bus.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("subscription did not recover")
	}
	if _, err := hub.BroadcastTenant(t.Context(), channel, tenant, "schema.updated", nil); err != nil {
		t.Fatal(err)
	}
	if env := readEnvelope(t, conn); env.Type != "schema.updated" {
		t.Fatalf("recovered subscription received %+v", env)
	}
}

func TestHub_RedisRejectsMalformedBroadcastsAndPreservesPayload(t *testing.T) {
	hub, client := redisHub(t)
	tenant := uuid.New().String()
	conn := dial(t, testServerAs(t, hub, tenant, "user-1"))
	subscribe(t, conn, NotificationsChannel)
	waitForSubscriber(t, hub, NotificationsChannel)

	for _, raw := range []string{
		"{",
		fmt.Sprintf(`{"channel":"notifications","tenant_id":%q,"type":"private","payload":null}`, tenant),
		`{"channel":"notifications","user_id":"user-1","type":"private","payload":null}`,
	} {
		if err := client.Publish(t.Context(), broadcastChannel, raw); err != nil {
			t.Fatal(err)
		}
	}
	const payload = uint64(18446744073709551615)
	if _, err := hub.BroadcastUser(t.Context(), NotificationsChannel, tenant, "user-1", "barrier", payload); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Type    string `json:"type"`
		Payload uint64 `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Type != "barrier" || env.Payload != payload {
		t.Fatalf("received %s: %v", raw, err)
	}
}
