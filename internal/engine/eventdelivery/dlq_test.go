package eventdelivery

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue/jobqueuetest"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdbtest"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

func TestDLQSubscriberLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int32
		attempt int
	}{
		{name: "exhausted", status: 1, attempt: 3},
		{name: "permanent", status: 2, attempt: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, err := pgxpool.New(t.Context(), localPostgresDSN)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			if err := pool.Ping(t.Context()); err != nil {
				t.Skipf("Postgres unavailable: %v", err)
			}
			driver := riverpgxv5.New(pool)
			schema := riverdbtest.TestSchema(t.Context(), t, driver, &riverdbtest.TestSchemaOpts{DisableReuse: true})
			dlq := NewDLQ(pool, schema)
			worker := newTestSubscriberWorker(t, buildHandleEventConstStatusModule(tc.status))
			modules := worker.ModuleRegistry.Snapshot().Modules()
			modules[testEventModuleName].Manifest.Subscribes[0].RetryPolicy = &manifest.RetryPolicy{
				MaxAttempts: 3, Backoff: "linear", InitialDelayMS: 100, Jitter: new(false),
			}
			if _, err := worker.ModuleRegistry.Update(modules); err != nil {
				t.Fatal(err)
			}

			startClient := func(worker *SubscriberDeliveryWorker) *river.Client[pgx.Tx] {
				t.Helper()
				workers := river.NewWorkers()
				river.AddWorker(workers, worker)
				client, err := jobqueuetest.New(driver, schema, &config.Config{
					QueueEventsConcurrency: 1, QueueDefaultConcurrency: 1,
				}, workers)
				if err != nil {
					t.Fatal(err)
				}
				if err := client.Start(t.Context()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := client.Stop(ctx); err != nil {
						t.Error(err)
					}
				})
				return client
			}
			client := startClient(worker)
			payload, err := msgpack.Marshal(map[string]any{"order_id": "order-1"})
			if err != nil {
				t.Fatal(err)
			}
			inserted, err := client.Insert(t.Context(), jobqueue.SubscriberDeliveryArgs{
				EventID: "event-1", EventName: testEventName, EventVersion: 1,
				ModuleName: testEventModuleName, HandlerName: testHandlerName, Payload: payload,
			}, &river.InsertOpts{MaxAttempts: 3})
			if err != nil {
				t.Fatal(err)
			}
			id := inserted.Job.ID
			waitState := func(client *river.Client[pgx.Tx], want rivertype.JobState) *rivertype.JobRow {
				t.Helper()
				deadline := time.NewTimer(10 * time.Second)
				defer deadline.Stop()
				for {
					row, err := client.JobGet(t.Context(), id)
					if err != nil {
						t.Fatal(err)
					}
					if row.State == want {
						return row
					}
					select {
					case <-deadline.C:
						t.Fatalf("job state = %s, want %s", row.State, want)
					case <-time.After(10 * time.Millisecond):
					}
				}
			}

			row := waitState(client, rivertype.JobStateDiscarded)
			if row.Attempt != tc.attempt || row.MaxAttempts != 3 || len(row.Errors) != tc.attempt {
				t.Fatalf("discarded attempts = %d/%d, errors = %d", row.Attempt, row.MaxAttempts, len(row.Errors))
			}
			page, err := dlq.List(t.Context(), DLQFilter{}, 50, 0)
			if err != nil || page.Total != 1 || len(page.Items) != 1 {
				t.Fatalf("DLQ page = %+v, %v", page, err)
			}
			if page.Items[0].Attempts != tc.attempt || page.Items[0].LastError == "" {
				t.Fatalf("DLQ item = %+v", page.Items[0])
			}
			if err := client.Stop(t.Context()); err != nil {
				t.Fatal(err)
			}

			count, err := dlq.Retry(t.Context(), DLQFilter{}, id)
			if err != nil || count != 1 {
				t.Fatalf("retry = %d, %v", count, err)
			}
			retried, err := client.JobGet(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if retried.Attempt != 0 || retried.MaxAttempts != 3 || retried.Queue != jobqueue.QueueDefault {
				t.Fatalf("fresh retry = %+v", retried)
			}

			success := newTestSubscriberWorker(t, buildHandleEventConstStatusModule(0))
			client = startClient(success)
			completed := waitState(client, rivertype.JobStateCompleted)
			if completed.Attempt != 1 {
				t.Fatalf("successful retry attempt = %d, want 1", completed.Attempt)
			}
			page, err = dlq.List(t.Context(), DLQFilter{}, 50, 0)
			if err != nil || page.Total != 0 || len(page.Items) != 0 {
				t.Fatalf("DLQ after success = %+v, %v", page, err)
			}
		})
	}
}

func TestDLQPayloadPreview(t *testing.T) {
	for _, tc := range []struct {
		name string
		data any
		want string
	}{
		{name: "object", data: map[string]any{"order_id": "order-1"}, want: `{"order_id":"order-1"}`},
		{name: "large payload", data: strings.Repeat("x", 5000), want: `"truncated":true`},
		{name: "large JSON", data: strings.Repeat("\x00", 1000), want: `"truncated":true`},
		{name: "HTML escaping", data: strings.Repeat("<", 1000), want: `"truncated":true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := msgpack.Marshal(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(dlqPayloadPreview(payload))
			if err != nil || !strings.Contains(string(got), tc.want) {
				t.Fatalf("preview = %s, %v", got, err)
			}
		})
	}
	if got := dlqPayloadPreview([]byte{0xc1}); got != nil {
		t.Fatalf("invalid MessagePack preview = %v, want nil", got)
	}
}
