package jobs

import (
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
	if got != want {
		t.Fatalf("buildOptions = %+v, want %+v", got, want)
	}
}

func TestBuildOptions_NoneLeavesZeroValue(t *testing.T) {
	if got := buildOptions(nil); got != (abi.JobEnqueueOptions{}) {
		t.Fatalf("buildOptions(nil) = %+v, want zero value", got)
	}
}
