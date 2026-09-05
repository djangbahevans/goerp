package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

// testServer wires a Hub behind a real listening HTTP server, accepting
// every connection unconditionally (auth/tenant resolution is
// dispatchWSRoute's job, not this package's) so tests can dial real
// *websocket.Conn clients against it.
func testServer(t *testing.T, hub *Hub) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_ = hub.Serve(r.Context(), conn, uuid.NewString(), "user-1", "tenant-1", "test-agent")
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

	if _, err := hub.Broadcast(t.Context(), "notifications", "notification.new", map[string]string{"id": "n1"}); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	env := readEnvelope(t, conn)
	if env.Channel != "notifications" || env.Type != "notification.new" {
		t.Errorf("got envelope %+v, want channel=notifications type=notification.new", env)
	}
}

func TestHub_TwoIndependentSubscribersBothReceive(t *testing.T) {
	hub := NewHub()
	url := testServer(t, hub)
	connA := dial(t, url)
	connB := dial(t, url)
	subscribe(t, connA, "feed:sales")
	subscribe(t, connB, "feed:sales")

	waitForSubscriberCount(t, hub, "feed:sales", 2)

	reached, err := hub.Broadcast(t.Context(), "feed:sales", "record.updated", nil)
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if reached != 2 {
		t.Errorf("reached = %d, want 2", reached)
	}

	envA := readEnvelope(t, connA)
	envB := readEnvelope(t, connB)
	if envA.Channel != "feed:sales" || envB.Channel != "feed:sales" {
		t.Errorf("both subscribers should receive on feed:sales, got %+v and %+v", envA, envB)
	}
}

func TestHub_UnsubscribeStopsDeliveryWithoutAffectingOtherSubscriber(t *testing.T) {
	hub := NewHub()
	url := testServer(t, hub)
	connA := dial(t, url)
	connB := dial(t, url)
	subscribe(t, connA, "feed:sales")
	subscribe(t, connB, "feed:sales")
	waitForSubscriberCount(t, hub, "feed:sales", 2)

	unsubscribe(t, connA, "feed:sales")
	waitForSubscriberCount(t, hub, "feed:sales", 1)

	reached, err := hub.Broadcast(t.Context(), "feed:sales", "record.updated", nil)
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if reached != 1 {
		t.Errorf("reached = %d, want 1 (only connB still subscribed)", reached)
	}
	_ = readEnvelope(t, connB) // connB still receives it
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

func TestHub_BroadcastToChannelWithNoSubscribersReachesZero(t *testing.T) {
	hub := NewHub()
	reached, err := hub.Broadcast(t.Context(), "nobody-subscribed", "record.updated", nil)
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if reached != 0 {
		t.Errorf("reached = %d, want 0", reached)
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

func waitForSubscriber(t *testing.T, hub *Hub, channel string) {
	t.Helper()
	waitForSubscriberCount(t, hub, channel, 1)
}
