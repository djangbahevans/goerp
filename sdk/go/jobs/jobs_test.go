package jobs

import (
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/jobs/def"
)

func TestWithProviderModule(t *testing.T) {
	got := def.BuildOptions([]JobOption{WithProviderModule("connector_paystack"), WithPriority(90)})
	if got.ProviderModule != "connector_paystack" || got.Opts.Priority != 90 {
		t.Fatalf("BuildOptions = %+v, want provider module and priority set", got)
	}
}

func TestDefEnqueue_RejectsProviderModule(t *testing.T) {
	job := Define[struct{}]("contacts_import", Label("Import"))

	if _, err := job.Enqueue(struct{}{}, WithProviderModule("connector_paystack")); err != def.ErrProviderModuleOption {
		t.Fatalf("Enqueue error = %v, want %v", err, def.ErrProviderModuleOption)
	}
}

func TestWithSyncTimeout(t *testing.T) {
	var in abi.JobsDispatchProviderSyncInput
	WithSyncTimeout(2500 * time.Millisecond)(&in)
	if in.TimeoutMs != 2500 {
		t.Fatalf("TimeoutMs = %d, want 2500", in.TimeoutMs)
	}
}
