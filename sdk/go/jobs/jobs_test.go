package jobs

import (
	"errors"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestBuildOptions(t *testing.T) {
	at := time.Unix(1_900_000_000, 0)
	got := buildOptions([]JobOption{
		OnQueue(QueueBulk),
		WithPriority(80),
		WithDelay(1500 * time.Millisecond),
		ScheduleAt(at),
		WithMaxAttempts(5),
		WithIdempotencyKey("import:file-1"),
	})
	want := abi.JobEnqueueOptions{
		Queue: QueueBulk, Priority: 80, DelayMs: 1500, ScheduledAt: at.Unix(),
		MaxAttempts: 5, IdempotencyKey: "import:file-1",
	}
	if got.opts != want || got.providerModule != "" {
		t.Fatalf("buildOptions = %+v, want %+v", got, want)
	}
}

func TestBuildOptions_NoneLeavesZeroValue(t *testing.T) {
	if got := buildOptions(nil); got != (enqueueOptions{}) {
		t.Fatalf("buildOptions(nil) = %+v, want zero value", got)
	}
}

func TestBuildOptions_ProviderModule(t *testing.T) {
	got := buildOptions([]JobOption{WithProviderModule("connector_paystack"), WithPriority(90)})
	if got.providerModule != "connector_paystack" || got.opts.Priority != 90 {
		t.Fatalf("buildOptions = %+v, want provider module and priority set", got)
	}
}

func TestEnqueue_RejectsProviderModule(t *testing.T) {
	if _, err := Enqueue("contacts_import", nil, WithProviderModule("connector_paystack")); !errors.Is(err, errProviderModuleOption) {
		t.Fatalf("Enqueue error = %v, want %v", err, errProviderModuleOption)
	}
}

func TestWithSyncTimeout(t *testing.T) {
	var in abi.JobsDispatchProviderSyncInput
	WithSyncTimeout(2500 * time.Millisecond)(&in)
	if in.TimeoutMs != 2500 {
		t.Fatalf("TimeoutMs = %d, want 2500", in.TimeoutMs)
	}
}
