package ws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func testServer(t *testing.T, hub *Hub) string {
	t.Helper()
	return testServerAs(t, hub, "tenant-1", "user-1")
}

func testServerAs(t *testing.T, hub *Hub, tenantID, userID string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_ = hub.Serve(r.Context(), conn, uuid.New().String(), userID, tenantID, "test-agent", AccessChecks{})
	}))
	t.Cleanup(srv.Close)
	return "ws://" + srv.Listener.Addr().String()
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func subscribe(t *testing.T, conn *websocket.Conn, channel string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, conn, inboundMessage{Type: "subscribe", Channel: channel}); err != nil {
		t.Fatalf("subscribe write: %v", err)
	}
}

func unsubscribe(t *testing.T, conn *websocket.Conn, channel string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, conn, inboundMessage{Type: "unsubscribe", Channel: channel}); err != nil {
		t.Fatalf("unsubscribe write: %v", err)
	}
}

func readEnvelope(t *testing.T, conn *websocket.Conn) outboundEnvelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var env outboundEnvelope
	if err := wsjson.Read(ctx, conn, &env); err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	return env
}

func TestHub_BroadcastDeliversToSubscriber(t *testing.T) {
	hub := NewHub()
	url := testServer(t, hub)
	conn := dial(t, url)
	subscribe(t, conn, "notifications")

	waitForSubscriber(t, hub, "notifications")

	if _, err := hub.BroadcastUser(t.Context(), "notifications", "tenant-1", "user-1", "notification.new", map[string]string{"id": "n1"}); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	env := readEnvelope(t, conn)
	if env.Channel != "notifications" || env.Type != "notification.new" {
		t.Errorf("got envelope %+v, want channel=notifications type=notification.new", env)
	}
}

func TestHub_BroadcastUserReachesOnlyThatUsersSessionsInTheTenant(t *testing.T) {
	hub := NewHub()
	target := dial(t, testServerAs(t, hub, "tenant-a", "user-1"))
	others := []*websocket.Conn{
		dial(t, testServerAs(t, hub, "tenant-b", "user-1")),
		dial(t, testServerAs(t, hub, "tenant-a", "user-2")),
	}
	subscribe(t, target, NotificationsChannel)
	for _, c := range others {
		subscribe(t, c, NotificationsChannel)
	}
	waitForSubscriberCount(t, hub, NotificationsChannel, 3)

	n, err := hub.BroadcastUser(t.Context(), NotificationsChannel, "tenant-a", "user-1", "notification.new", map[string]string{"id": "n1"})
	if err != nil {
		t.Fatalf("BroadcastUser: %v", err)
	}
	if n != 1 {
		t.Errorf("reached %d sessions, want 1", n)
	}
	if env := readEnvelope(t, target); env.Channel != NotificationsChannel || env.Type != "notification.new" {
		t.Errorf("target session got %+v, want notification.new on %s", env, NotificationsChannel)
	}

	for i, c := range others {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		var env outboundEnvelope
		if err := wsjson.Read(ctx, c, &env); err == nil {
			t.Errorf("other session %d got %+v, want nothing", i, env)
		}
		cancel()
	}
}

func TestHub_TwoIndependentSubscribersBothReceive(t *testing.T) {
	hub := NewHub()
	url := testServer(t, hub)
	connA := dial(t, url)
	connB := dial(t, url)
	subscribe(t, connA, "tenant:tenant-1")
	subscribe(t, connB, "tenant:tenant-1")

	waitForSubscriberCount(t, hub, "tenant:tenant-1", 2)

	reached, err := hub.BroadcastTenant(t.Context(), "tenant:tenant-1", "tenant-1", "record.updated", nil)
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if reached != 2 {
		t.Errorf("reached = %d, want 2", reached)
	}

	envA := readEnvelope(t, connA)
	envB := readEnvelope(t, connB)
	if envA.Channel != "tenant:tenant-1" || envB.Channel != "tenant:tenant-1" {
		t.Errorf("both subscribers should receive on tenant:tenant-1, got %+v and %+v", envA, envB)
	}
}

func TestHub_UnsubscribeStopsDeliveryWithoutAffectingOtherSubscriber(t *testing.T) {
	hub := NewHub()
	url := testServer(t, hub)
	connA := dial(t, url)
	connB := dial(t, url)
	subscribe(t, connA, "tenant:tenant-1")
	subscribe(t, connB, "tenant:tenant-1")
	waitForSubscriberCount(t, hub, "tenant:tenant-1", 2)

	unsubscribe(t, connA, "tenant:tenant-1")
	waitForSubscriberCount(t, hub, "tenant:tenant-1", 1)

	reached, err := hub.BroadcastTenant(t.Context(), "tenant:tenant-1", "tenant-1", "record.updated", nil)
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if reached != 1 {
		t.Errorf("reached = %d, want 1 (only connB still subscribed)", reached)
	}
	_ = readEnvelope(t, connB)
}

func TestHub_DisconnectRemovesConnectionFromRegistryAndChannels(t *testing.T) {
	hub := NewHub()
	url := testServer(t, hub)
	conn := dial(t, url)
	subscribe(t, conn, "notifications")
	waitForSubscriberCount(t, hub, "notifications", 1)

	if err := conn.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatalf("Close: %v", err)
	}

	waitForSubscriberCount(t, hub, "notifications", 0)

	hub.mu.RLock()
	connCount := len(hub.conns)
	hub.mu.RUnlock()
	if connCount != 0 {
		t.Errorf("hub still has %d registered connection(s) after disconnect, want 0", connCount)
	}
}

func TestHub_CloseClosesEveryRegisteredConnection(t *testing.T) {
	hub := NewHub()
	url := testServer(t, hub)
	connA := dial(t, url)
	connB := dial(t, url)
	subscribe(t, connA, "notifications")
	subscribe(t, connB, "notifications")
	waitForSubscriberCount(t, hub, "notifications", 2)

	// A real client keeps reading for the life of the connection (the
	// library's own requirement — otherwise control frames, including
	// the close handshake Hub.Close initiates, never get handled). Both
	// connections just discard whatever arrives; readErr records how
	// each connection's read loop ultimately ended.
	readErr := make(chan error, 2)
	for _, c := range []*websocket.Conn{connA, connB} {
		go func(c *websocket.Conn) {
			for {
				if _, _, err := c.Read(t.Context()); err != nil {
					readErr <- err
					return
				}
			}
		}(c)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	hub.Close(ctx)

	for range 2 {
		select {
		case err := <-readErr:
			if err == nil {
				t.Error("expected a close error, got nil")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for both connections to observe the close")
		}
	}

	waitForConnCount(t, hub, 0)
}

func TestHub_BroadcastToChannelWithNoSubscribersReachesZero(t *testing.T) {
	hub := NewHub()
	reached, err := hub.BroadcastTenant(t.Context(), "tenant:tenant-1", "tenant-1", "record.updated", nil)
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if reached != 0 {
		t.Errorf("reached = %d, want 0", reached)
	}
}

func TestTenantChannel_FormatsTenantPrefix(t *testing.T) {
	if got, want := TenantChannel("t1"), "tenant:t1"; got != want {
		t.Errorf("TenantChannel(%q) = %q, want %q", "t1", got, want)
	}
}

func TestUserChannel_FormatsUIUserPrefix(t *testing.T) {
	if got, want := UserChannel("u1"), "ui:user:u1"; got != want {
		t.Errorf("UserChannel(%q) = %q, want %q", "u1", got, want)
	}
}

// waitForSubscriberCount polls the hub's internal state until the given
// channel has exactly n subscribers or the deadline elapses — subscribe/
// unsubscribe/disconnect are handled by each connection's own read-loop
// goroutine inside Hub.Serve, so a test issuing a subscribe/close and then
// immediately broadcasting has no other synchronization point to wait on.
func waitForSubscriberCount(t *testing.T, hub *Hub, channel string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		got := len(hub.subscribers[channel])
		hub.mu.RUnlock()
		if got == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("channel %q subscriber count did not reach %d in time", channel, n)
}

// waitForConnCount polls rather than asserting once: Serve unregisters a
// connection on its own goroutine after its read loop ends, which can be after
// the client has already observed the close.
func waitForConnCount(t *testing.T, hub *Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		got := len(hub.conns)
		hub.mu.RUnlock()
		if got == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("hub connection count did not reach %d in time", n)
}

func waitForSubscriber(t *testing.T, hub *Hub, channel string) {
	t.Helper()
	waitForSubscriberCount(t, hub, channel, 1)
}

func TestHub_RefusesUnauthorizedChannelsAndKeepsConnectionOpen(t *testing.T) {
	for _, channel := range []string{
		"tenant:other", "ui:user:other", "ui:role:admin", "ui:role:", "",
		"feed:contacts", "record:contacts.contact:1", "ui:record:contacts.contact:1", "unknown",
	} {
		t.Run(channel, func(t *testing.T) {
			hub := NewHub()
			conn := dial(t, testServer(t, hub))
			subscribe(t, conn, channel)

			env := readEnvelope(t, conn)
			if env.Channel != channel || env.Type != "subscription.denied" {
				t.Fatalf("denial = %+v", env)
			}
			payload, ok := env.Payload.(map[string]any)
			if !ok || payload["code"] != "ws.subscription_forbidden" {
				t.Fatalf("denial payload = %#v", env.Payload)
			}
			waitForSubscriberCount(t, hub, channel, 0)
			if n, err := hub.BroadcastTenant(t.Context(), channel, "tenant-1", "private", nil); err != nil || n != 0 {
				t.Fatalf("refused channel reached %d: %v", n, err)
			}

			subscribe(t, conn, NotificationsChannel)
			waitForSubscriber(t, hub, NotificationsChannel)
			if _, err := hub.BroadcastUser(t.Context(), NotificationsChannel, "tenant-1", "user-1", "probe", nil); err != nil {
				t.Fatal(err)
			}
			if env := readEnvelope(t, conn); env.Type != "probe" {
				t.Fatalf("connection after denial = %+v", env)
			}
		})
	}
}

func TestHub_OwnTenantAndUserChannelsRemainAvailable(t *testing.T) {
	hub := NewHub()
	conn := dial(t, testServer(t, hub))
	for _, channel := range []string{TenantChannel("tenant-1"), UserChannel("user-1")} {
		subscribe(t, conn, channel)
		waitForSubscriber(t, hub, channel)
		if n, err := hub.BroadcastTenant(t.Context(), channel, "tenant-1", "probe", nil); err != nil || n != 1 {
			t.Fatalf("broadcast on %s reached %d: %v", channel, n, err)
		}
		if env := readEnvelope(t, conn); env.Channel != channel || env.Type != "probe" {
			t.Fatalf("own channel = %+v", env)
		}
	}
}

func testServerWithRoles(t *testing.T, hub *Hub, tenantID string, roles RoleChecker) string {
	t.Helper()
	return testServerWithAccess(t, hub, tenantID, AccessChecks{Role: roles})
}

func testServerWithAccess(t *testing.T, hub *Hub, tenantID string, access AccessChecks) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_ = hub.Serve(r.Context(), conn, uuid.New().String(), "user-1", tenantID, "test-agent", access)
	}))
	t.Cleanup(srv.Close)
	return "ws://" + srv.Listener.Addr().String()
}

func TestHub_RoleDeliveryChecksLiveGrantsAndTenant(t *testing.T) {
	for _, lookupFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("lookupFails=%t", lookupFails), func(t *testing.T) {
			hub := NewHub()
			var revoked atomic.Bool
			roles := func(_ context.Context, name string) (bool, error) {
				if revoked.Load() && lookupFails {
					return false, errors.New("role database unavailable")
				}
				return name == "admin" && !revoked.Load(), nil
			}
			conn := dial(t, testServerWithRoles(t, hub, "tenant-1", roles))
			other := dial(t, testServerWithRoles(t, hub, "tenant-2", roles))
			const channel = "ui:role:admin"
			for _, c := range []*websocket.Conn{conn, other} {
				subscribe(t, c, channel)
				subscribe(t, c, NotificationsChannel)
			}
			waitForSubscriberCount(t, hub, channel, 2)
			waitForSubscriberCount(t, hub, NotificationsChannel, 2)

			if n, err := hub.BroadcastTenant(t.Context(), channel, "tenant-1", "private", nil); err != nil || n != 1 {
				t.Fatalf("role broadcast reached %d: %v", n, err)
			}
			if env := readEnvelope(t, conn); env.Type != "private" {
				t.Fatalf("role message = %+v", env)
			}
			if _, err := hub.BroadcastUser(t.Context(), NotificationsChannel, "tenant-2", "user-1", "barrier", nil); err != nil {
				t.Fatal(err)
			}
			if env := readEnvelope(t, other); env.Type != "barrier" {
				t.Fatalf("other tenant received role payload: %+v", env)
			}

			revoked.Store(true)
			if n, err := hub.BroadcastTenant(t.Context(), channel, "tenant-1", "private-after-revoke", nil); err != nil || n != 0 {
				t.Fatalf("revoked role reached %d: %v", n, err)
			}
			waitForSubscriberCount(t, hub, channel, 1)
			if _, err := hub.BroadcastUser(t.Context(), NotificationsChannel, "tenant-1", "user-1", "barrier", nil); err != nil {
				t.Fatal(err)
			}
			if env := readEnvelope(t, conn); env.Type != "barrier" {
				t.Fatalf("revoked role received payload: %+v", env)
			}

			subscribe(t, conn, channel)
			if env := readEnvelope(t, conn); env.Type != "subscription.denied" {
				t.Fatalf("revoked role subscribe = %+v", env)
			}
		})
	}
}

func TestHub_BroadcastRechecksInjectedUnauthorizedSubscription(t *testing.T) {
	hub := NewHub()
	conn := dial(t, testServer(t, hub))
	subscribe(t, conn, NotificationsChannel)
	waitForSubscriber(t, hub, NotificationsChannel)

	for _, channel := range []string{TenantChannel("other"), UserChannel("other"), "unknown"} {
		hub.mu.Lock()
		c := slices.Collect(maps.Values(hub.conns))[0]
		hub.subscribers[channel] = map[string]*Conn{c.ID: c}
		hub.mu.Unlock()

		if n, err := hub.BroadcastTenant(t.Context(), channel, "tenant-1", "private", nil); err != nil || n != 0 {
			t.Fatalf("injected channel %s reached %d: %v", channel, n, err)
		}
	}

	if _, err := hub.BroadcastUser(t.Context(), NotificationsChannel, "tenant-1", "user-1", "barrier", nil); err != nil {
		t.Fatal(err)
	}
	if env := readEnvelope(t, conn); env.Type != "barrier" {
		t.Fatalf("unauthorized payload received: %+v", env)
	}
}

func TestHub_TenantBroadcastRejectsSharedNotificationsChannel(t *testing.T) {
	hub := NewHub()
	if n, err := hub.BroadcastTenant(t.Context(), NotificationsChannel, "tenant-1", "notification.new", nil); err == nil || n != 0 {
		t.Fatalf("tenant notification broadcast reached %d: %v", n, err)
	}
}

func TestHub_AnonymousIdentityCannotSubscribe(t *testing.T) {
	for _, identity := range []struct{ tenant, user string }{
		{tenant: "tenant-1"},
		{user: "user-1"},
	} {
		hub := NewHub()
		conn := dial(t, testServerAs(t, hub, identity.tenant, identity.user))
		subscribe(t, conn, NotificationsChannel)
		if env := readEnvelope(t, conn); env.Type != "subscription.denied" {
			t.Fatalf("missing identity admitted: %+v", env)
		}
		waitForSubscriberCount(t, hub, NotificationsChannel, 0)
	}
}
