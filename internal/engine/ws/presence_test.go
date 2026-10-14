package ws

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/djangbahevans/goerp/internal/engine/cache"
)

func presenceClient(t *testing.T, hub *Hub, tenant, user string, allowed func() (bool, error)) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_ = hub.Serve(r.Context(), conn, uuid.New().String(), user, tenant, "presence-test", AccessChecks{
			ReadRecord: func(context.Context, string, string) (bool, error) {
				if allowed != nil {
					return allowed()
				}
				return true, nil
			},
			PresenceProfile: func(context.Context) (PresenceProfile, error) {
				return PresenceProfile{Name: user + " name", AvatarURL: new("https://avatar.test/" + user)}, nil
			},
		})
	}))
	t.Cleanup(srv.Close)
	return dial(t, "ws://"+srv.Listener.Addr().String())
}

func sendPresence(t *testing.T, conn *websocket.Conn, kind, channel string, field *string) {
	t.Helper()
	if err := wsjson.Write(t.Context(), conn, inboundMessage{Type: kind, Channel: channel, Field: field}); err != nil {
		t.Fatal(err)
	}
}

func readPresence(t *testing.T, conn *websocket.Conn, channel string, accept func([]presenceUser) bool) []presenceUser {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		var env struct {
			Channel string `json:"channel"`
			Type    string `json:"type"`
			Payload struct {
				Resource string         `json:"resource"`
				RecordID string         `json:"record_id"`
				Users    []presenceUser `json:"users"`
			} `json:"payload"`
		}
		if err := wsjson.Read(ctx, conn, &env); err != nil {
			t.Fatal(err)
		}
		resource, id, _ := recordTarget(channel)
		if env.Type != "presence.update" || env.Channel != channel || env.Payload.Resource != resource || env.Payload.RecordID != id {
			t.Fatalf("presence envelope = %+v", env)
		}
		if accept(env.Payload.Users) {
			return env.Payload.Users
		}
	}
}

func presenceEntries(t *testing.T, client *cache.Client, key presenceKey) map[string]presenceEntry {
	t.Helper()
	fields, _, err := client.GetHash(t.Context(), key.keys()[0])
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]presenceEntry)
	for id, raw := range fields {
		var entry presenceEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			t.Fatal(err)
		}
		entries[id] = entry
	}
	return entries
}

func TestPresence_TwoInstancesMergeTabsAndCleanUp(t *testing.T) {
	a, client := redisHub(t)
	b, _ := redisHub(t)
	key := presenceKey{tenantID: uuid.New().String(), channel: "record:sales.order:1"}
	first := presenceClient(t, a, key.tenantID, "user-a", nil)
	second := presenceClient(t, b, key.tenantID, "user-b", nil)
	sendPresence(t, first, "presence.join", key.channel, nil)
	initial := readPresence(t, first, key.channel, func(users []presenceUser) bool { return len(users) == 1 })
	if initial[0].Name != "user-a name" || initial[0].AvatarURL == nil || *initial[0].AvatarURL != "https://avatar.test/user-a" || initial[0].EditingField != nil {
		t.Fatalf("resolved profile = %+v", initial)
	}
	sendPresence(t, second, "presence.join", key.channel, nil)
	for _, conn := range []*websocket.Conn{first, second} {
		users := readPresence(t, conn, key.channel, func(users []presenceUser) bool { return len(users) == 2 })
		if users[0].UserID != "user-a" || users[1].UserID != "user-b" {
			t.Fatalf("join order = %+v", users)
		}
	}

	tab := presenceClient(t, b, key.tenantID, "user-a", nil)
	sendPresence(t, tab, "presence.join", key.channel, nil)
	readPresence(t, tab, key.channel, func(users []presenceUser) bool { return len(users) == 2 })
	sendPresence(t, first, "presence.focus", key.channel, new("price"))
	readPresence(t, second, key.channel, func(users []presenceUser) bool {
		return users[0].EditingField != nil && *users[0].EditingField == "price"
	})
	sendPresence(t, tab, "presence.focus", key.channel, new("quantity"))
	users := readPresence(t, second, key.channel, func(users []presenceUser) bool {
		return users[0].EditingField != nil && *users[0].EditingField == "quantity"
	})
	if !users[0].JoinedAt.Equal(initial[0].JoinedAt) || !users[0].LastSeenAt.After(initial[0].LastSeenAt) || len(presenceEntries(t, client, key)) != 3 {
		t.Fatalf("merged timestamps or connection count = %+v", users)
	}
	sendPresence(t, tab, "presence.focus", key.channel, nil)
	readPresence(t, second, key.channel, func(users []presenceUser) bool { return users[0].EditingField == nil })

	sendPresence(t, tab, "presence.leave", key.channel, nil)
	readPresence(t, second, key.channel, func(users []presenceUser) bool {
		return users[0].EditingField != nil && *users[0].EditingField == "price"
	})
	if len(presenceEntries(t, client, key)) != 2 {
		t.Fatal("leaving tab retained its Redis entry")
	}
	unsubscribe(t, first, key.channel)
	readPresence(t, second, key.channel, func(users []presenceUser) bool { return len(users) == 1 && users[0].UserID == "user-b" })
	_ = second.CloseNow()
	readPresence(t, tab, key.channel, func(users []presenceUser) bool { return len(users) == 0 })
	if len(presenceEntries(t, client, key)) != 0 {
		t.Fatal("disconnect retained a Redis entry")
	}
}

func TestPresence_AdmissionAndDeliveryRevocation(t *testing.T) {
	for _, lookupFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("lookupFails=%t", lookupFails), func(t *testing.T) {
			hub, client := redisHub(t)
			key := presenceKey{tenantID: uuid.New().String(), channel: "record:sales.order:1"}
			var revoked atomic.Bool
			allowed := func() (bool, error) {
				if revoked.Load() && lookupFails {
					return false, errors.New("read lookup failed")
				}
				return !revoked.Load(), nil
			}
			conn := presenceClient(t, hub, key.tenantID, "target", allowed)
			observer := presenceClient(t, hub, key.tenantID, "observer", nil)
			sendPresence(t, observer, "presence.join", key.channel, nil)
			readPresence(t, observer, key.channel, func(users []presenceUser) bool { return len(users) == 1 })
			revoked.Store(true)
			sendPresence(t, conn, "presence.join", key.channel, nil)
			if env := readEnvelope(t, conn); env.Type != "subscription.denied" {
				t.Fatalf("denial = %+v", env)
			}
			if len(presenceEntries(t, client, key)) != 1 {
				t.Fatal("refused join created presence")
			}
			revoked.Store(false)
			sendPresence(t, conn, "presence.join", key.channel, nil)
			readPresence(t, conn, key.channel, func(users []presenceUser) bool { return len(users) == 2 })
			readPresence(t, observer, key.channel, func(users []presenceUser) bool { return len(users) == 2 })
			revoked.Store(true)
			if _, err := hub.BroadcastTenant(t.Context(), key.channel, key.tenantID, "record.updated", nil); err != nil {
				t.Fatal(err)
			}
			if env := readEnvelope(t, observer); env.Type != "record.updated" {
				t.Fatalf("trigger = %+v", env)
			}
			readPresence(t, observer, key.channel, func(users []presenceUser) bool { return len(users) == 1 && users[0].UserID == "observer" })
			if len(presenceEntries(t, client, key)) != 1 {
				t.Fatal("delivery revocation retained presence")
			}
		})
	}
}

func TestPresence_RefreshPrunesCrashedEntriesAndKeepsOwnEntries(t *testing.T) {
	hub, client := redisHub(t)
	key := presenceKey{tenantID: uuid.New().String(), channel: "record:sales.order:1"}
	conn := presenceClient(t, hub, key.tenantID, "live", nil)
	sendPresence(t, conn, "presence.join", key.channel, nil)
	readPresence(t, conn, key.channel, func(users []presenceUser) bool { return len(users) == 1 })
	now := time.Now().UTC()
	old := now.Add(-presenceLifetime).Format(presenceTimeFormat)
	raw := fmt.Sprintf(`{"user_id":"crashed","name":"Crashed","avatar_url":null,"editing_field":null,"focused_at":null,"joined_at":%q,"last_seen_at":%q}`, old, old)
	if _, err := client.Eval(t.Context(), `return redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])`, key.keys()[:1], "dead-connection", raw); err != nil {
		t.Fatal(err)
	}
	hub.refreshPresence(t.Context(), now)
	readPresence(t, conn, key.channel, func(users []presenceUser) bool { return len(users) == 1 && users[0].UserID == "live" })
	entries := presenceEntries(t, client, key)
	if len(entries) != 1 {
		t.Fatalf("stale entries = %+v", entries)
	}
	for _, entry := range entries {
		if !entry.LastSeenAt.Equal(now) {
			t.Fatalf("heartbeat = %s, want %s", entry.LastSeenAt, now)
		}
	}
	result, err := client.Eval(t.Context(), `return redis.call('TTL', KEYS[1])`, key.keys()[:1])
	if err != nil || result.(int64) < 119 || result.(int64) > 120 {
		t.Fatalf("presence TTL = %v: %v", result, err)
	}
}

func TestPresence_FocusUsesSlidingWindowAcrossRecords(t *testing.T) {
	hub, client := redisHub(t)
	tenant := uuid.New().String()
	channels := []string{"record:sales.order:1", "record:sales.order:2"}
	conn := presenceClient(t, hub, tenant, "user", nil)
	for _, channel := range channels {
		sendPresence(t, conn, "presence.join", channel, nil)
		readPresence(t, conn, channel, func(users []presenceUser) bool { return len(users) == 1 })
	}
	for i := range 10 {
		sendPresence(t, conn, "presence.focus", channels[i%2], new(fmt.Sprintf("field-%d", i)))
	}
	sendPresence(t, conn, "presence.focus", channels[0], new("dropped"))
	sendPresence(t, conn, "presence.join", "unknown", nil)
	for readEnvelope(t, conn).Type != "subscription.denied" {
	}
	for i, channel := range channels {
		for _, entry := range presenceEntries(t, client, presenceKey{tenantID: tenant, channel: channel}) {
			if entry.EditingField == nil || *entry.EditingField != fmt.Sprintf("field-%d", 8+i) {
				t.Fatalf("focus limit stored %+v", entry)
			}
		}
	}
}

func TestPresence_PublicationGateIsSharedAndRejectsStaleSnapshots(t *testing.T) {
	_, client := redisHub(t)
	key := presenceKey{tenantID: uuid.New().String(), channel: "record:sales.order:1"}
	msgs, closeFn, err := client.Subscribe(t.Context(), broadcastChannel)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeFn() }()
	updates := key.keys()[1:]
	if _, err := client.Eval(t.Context(), `return redis.call('HSET', KEYS[1], 'revision', 2, 'dirty', 1)`, updates); err != nil {
		t.Fatal(err)
	}
	if result, err := client.Eval(t.Context(), presencePublishScript, updates, "1", broadcastChannel, "stale"); err != nil || result != int64(0) {
		t.Fatalf("stale snapshot accepted: %v, %v", result, err)
	}
	var published atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			result, err := client.Eval(t.Context(), presencePublishScript, updates, "2", broadcastChannel, "fresh")
			if err != nil {
				t.Error(err)
			}
			if result == int64(1) {
				published.Add(1)
			}
		})
	}
	wg.Wait()
	if published.Load() != 20 {
		t.Fatal("publication or clean no-op failed")
	}
	select {
	case msg := <-msgs:
		if msg.Payload != "fresh" {
			t.Fatalf("message = %s", msg.Payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("presence was not published")
	}
	if _, err := client.Eval(t.Context(), `return redis.call('HSET', KEYS[1], 'revision', 3, 'dirty', 1)`, updates); err != nil {
		t.Fatal(err)
	}
	if result, err := client.Eval(t.Context(), presencePublishScript, updates, "3", broadcastChannel, "too-early"); err != nil || result != int64(0) {
		t.Fatalf("250ms gate accepted publication: %v, %v", result, err)
	}
	select {
	case msg := <-msgs:
		t.Fatalf("duplicate publication: %s", msg.Payload)
	default:
	}
}

func TestMergePresence_FocusAndOrdering(t *testing.T) {
	now := time.Now().UTC()
	entries := []presenceEntry{
		{UserID: "a", JoinedAt: now, LastSeenAt: now, EditingField: new("old"), FocusedAt: new(now)},
		{UserID: "b", JoinedAt: now.Add(-time.Second), LastSeenAt: now},
		{UserID: "a", JoinedAt: now.Add(time.Second), LastSeenAt: now.Add(2 * time.Second), FocusedAt: new(now.Add(time.Second))},
	}
	users := mergePresence(slices.Clone(entries))
	if len(users) != 2 || users[0].UserID != "b" || users[1].UserID != "a" || users[1].EditingField != nil || !users[1].JoinedAt.Equal(now) || !users[1].LastSeenAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("merged users = %+v", users)
	}
}

func TestPresence_ChannelScopeAndTenantIsolation(t *testing.T) {
	hub, client := redisHub(t)
	key := presenceKey{tenantID: uuid.New().String(), channel: "record:sales.order:1"}
	conn := presenceClient(t, hub, key.tenantID, "same-user", nil)
	otherTenant := uuid.New().String()
	other := presenceClient(t, hub, otherTenant, "same-user", nil)
	for _, channel := range []string{"notifications", "ui:record:sales.order:1", "record:sales.order:1:extra"} {
		sendPresence(t, conn, "presence.join", channel, nil)
		if env := readEnvelope(t, conn); env.Type != "subscription.denied" {
			t.Fatalf("invalid presence join = %+v", env)
		}
	}
	sendPresence(t, conn, "presence.focus", key.channel, new("unjoined"))
	sendPresence(t, conn, "presence.join", "unknown", nil)
	if env := readEnvelope(t, conn); env.Type != "subscription.denied" {
		t.Fatalf("barrier = %+v", env)
	}
	if len(presenceEntries(t, client, key)) != 0 {
		t.Fatal("focus without join created presence")
	}
	for _, c := range []*websocket.Conn{conn, other} {
		sendPresence(t, c, "presence.join", key.channel, nil)
		readPresence(t, c, key.channel, func(users []presenceUser) bool { return len(users) == 1 })
	}
	subscribe(t, conn, "ui:record:sales.order:1")
	waitForSubscriber(t, hub, "ui:record:sales.order:1")
	unsubscribe(t, conn, "ui:record:sales.order:1")
	waitForSubscriberCount(t, hub, "ui:record:sales.order:1", 0)
	sendPresence(t, conn, "presence.focus", key.channel, new("tenant-private"))
	readPresence(t, conn, key.channel, func(users []presenceUser) bool {
		return users[0].EditingField != nil && *users[0].EditingField == "tenant-private"
	})
	for _, entry := range presenceEntries(t, client, presenceKey{tenantID: otherTenant, channel: key.channel}) {
		if entry.EditingField != nil {
			t.Fatal("focus crossed tenants")
		}
	}
	subscribe(t, other, NotificationsChannel)
	waitForSubscriber(t, hub, NotificationsChannel)
	if _, err := hub.BroadcastUser(t.Context(), NotificationsChannel, otherTenant, "same-user", "barrier", nil); err != nil {
		t.Fatal(err)
	}
	if env := readEnvelope(t, other); env.Type != "barrier" {
		t.Fatalf("other tenant received presence: %+v", env)
	}
}

func TestPresence_ShutdownPublishesDepartureToOtherInstance(t *testing.T) {
	closing, client := redisHub(t)
	observerHub, _ := redisHub(t)
	key := presenceKey{tenantID: uuid.New().String(), channel: "record:sales.order:1"}
	conn := presenceClient(t, closing, key.tenantID, "leaving", nil)
	observer := presenceClient(t, observerHub, key.tenantID, "staying", nil)
	sendPresence(t, conn, "presence.join", key.channel, nil)
	readPresence(t, conn, key.channel, func(users []presenceUser) bool { return len(users) == 1 })
	sendPresence(t, observer, "presence.join", key.channel, nil)
	readPresence(t, conn, key.channel, func(users []presenceUser) bool { return len(users) == 2 })
	readPresence(t, observer, key.channel, func(users []presenceUser) bool { return len(users) == 2 })
	readDone := conn.CloseRead(t.Context())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	closing.Close(ctx)
	select {
	case <-readDone.Done():
	default:
		t.Fatal("shutdown did not close connection")
	}
	readPresence(t, observer, key.channel, func(users []presenceUser) bool { return len(users) == 1 && users[0].UserID == "staying" })
	if len(presenceEntries(t, client, key)) != 1 {
		t.Fatal("shutdown retained presence")
	}
}
