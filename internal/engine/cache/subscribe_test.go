package cache

import (
	"context"
	"testing"
	"time"
	"uuid"
)

func TestSubscribeConfirmsSubscriptionAndReleasesConnection(t *testing.T) {
	for _, stopByContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "cancel"}[stopByContext], func(t *testing.T) {
			client, err := New(t.Context(), localRedisConfig())
			skipIfUnreachable(t, err)
			t.Cleanup(func() { _ = client.Close() })

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			channel := "cache-test:" + uuid.New().String()
			msgs, closeFn, err := client.Subscribe(ctx, channel)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = closeFn() })

			if err := client.Publish(t.Context(), channel, "hello"); err != nil {
				t.Fatal(err)
			}
			select {
			case msg := <-msgs:
				if msg.Channel != channel || msg.Payload != "hello" {
					t.Fatalf("received %+v", msg)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("subscription was not ready for immediate publication")
			}

			if stopByContext {
				cancel()
			} else if err := closeFn(); err != nil {
				t.Fatal(err)
			}
			select {
			case _, open := <-msgs:
				if open {
					t.Fatal("subscription channel is still open")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("subscription did not stop")
			}

			deadline := time.Now().Add(5 * time.Second)
			for {
				counts, err := client.rdb.PubSubNumSub(t.Context(), channel).Result()
				if err != nil {
					t.Fatal(err)
				}
				if counts[channel] == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("Redis still has a subscription: %v", counts)
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

func TestSubscribeReportsHandshakeFailure(t *testing.T) {
	client, err := New(t.Context(), localRedisConfig())
	skipIfUnreachable(t, err)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	msgs, closeFn, err := client.Subscribe(t.Context(), "cache-test:"+uuid.New().String())
	if err == nil || msgs != nil || closeFn != nil {
		t.Fatalf("failed subscription returned %v, %v, %v", msgs, closeFn == nil, err)
	}
}
