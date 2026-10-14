package ws

import (
	"context"
	"encoding/json/v2"
	"maps"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type changeBus struct {
	BroadcastBus
	mu       sync.Mutex
	messages []broadcastMessage
}

func (b *changeBus) Publish(ctx context.Context, _ string, raw string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var message broadcastMessage
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		return err
	}
	b.mu.Lock()
	b.messages = append(b.messages, message)
	b.mu.Unlock()
	return nil
}

func TestHub_RecordChanged_ConcurrentWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := &changeBus{}
		hub := NewHub()
		hub.bus = bus
		defer hub.Close(t.Context())

		var wg sync.WaitGroup
		for range 100 {
			wg.Go(func() {
				hub.RecordChanged(t.Context(), "tenant-1", "contacts.contact", "record-1", "updated")
			})
		}
		wg.Wait()
		if len(bus.snapshot()) != 101 {
			t.Fatal("concurrent writes published more than one immediate feed message")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if len(bus.snapshot()) != 102 {
			t.Fatal("concurrent writes did not publish exactly one trailing feed message")
		}
	})
}

func (b *changeBus) snapshot() []broadcastMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.messages)
}

func TestHub_RecordChanged_CoalescesFeedAndPublishesEveryRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := &changeBus{}
		hub := NewHub()
		hub.bus = bus
		defer hub.Close(t.Context())

		for range 100 {
			hub.RecordChanged(t.Context(), "tenant-1", "contacts.contact", "record-1", "updated")
		}
		messages := bus.snapshot()
		if len(messages) != 101 {
			t.Fatalf("immediate messages = %d, want 1 feed and 100 record messages", len(messages))
		}
		for i, msg := range messages {
			wantChannel, wantType := "record:contacts.contact:record-1", "record.updated"
			want := map[string]string{"resource": "contacts.contact", "record_id": "record-1"}
			if i == 0 {
				wantChannel, wantType = "feed:contacts", "records.changed"
				delete(want, "record_id")
			}
			var payload map[string]string
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if msg.TenantID != "tenant-1" || msg.Channel != wantChannel || msg.Type != wantType || !maps.Equal(payload, want) {
				t.Fatalf("message %d = %+v with payload %v", i, msg, payload)
			}
		}

		time.Sleep(999 * time.Millisecond)
		synctest.Wait()
		if len(bus.snapshot()) != 101 {
			t.Fatal("feed published before the one-second window ended")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		messages = bus.snapshot()
		if len(messages) != 102 || messages[101].Type != "records.changed" {
			t.Fatalf("messages after trailing timer = %d, want 102 ending with records.changed", len(messages))
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if len(bus.snapshot()) != 102 || len(hub.feedWindows) != 0 {
			t.Fatal("idle feed retained a timer or sent an extra message")
		}
		hub.RecordChanged(t.Context(), "tenant-1", "contacts.contact", "", "created")
		if len(bus.snapshot()) != 103 {
			t.Fatal("next change did not open a new immediate window")
		}
	})
}

func TestHub_RecordChanged_IsolatesWindowsAndStopsOnClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := &changeBus{}
		hub := NewHub()
		hub.bus = bus
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		for _, target := range []feedKey{
			{tenantID: "tenant-1", resource: "contacts.contact"},
			{tenantID: "tenant-2", resource: "contacts.contact"},
			{tenantID: "tenant-1", resource: "contacts.address"},
		} {
			hub.RecordChanged(ctx, target.tenantID, target.resource, "", "created")
			hub.RecordChanged(ctx, target.tenantID, target.resource, "", "created")
		}
		if len(bus.snapshot()) != 3 {
			t.Fatal("different tenants or resources shared a coalescing window")
		}
		hub.RecordChanged(ctx, "tenant-1", "contacts.contact", "record-1", "deleted")
		messages := bus.snapshot()
		if len(messages) != 4 || messages[3].Type != "record.deleted" {
			t.Fatalf("delete messages = %+v", messages)
		}
		hub.Close(t.Context())
		hub.Close(t.Context())
		hub.RecordChanged(ctx, "tenant-1", "contacts.contact", "record-1", "updated")
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if len(bus.snapshot()) != 4 || len(hub.feedWindows) != 0 {
			t.Fatal("hub published or retained pending changes after shutdown")
		}
	})
}
