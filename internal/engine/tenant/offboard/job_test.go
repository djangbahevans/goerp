package tenantoffboard

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/riverqueue/river"
)

// River retries rerun Work from the start, so partial offboarding and repeated status
// changes must remain idempotent.
func TestImmediateWorker_RetryAfterPartialCompletionIsIdempotent(t *testing.T) {
	env := newTestEnv(t, nil)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)
	ctx := t.Context()

	w := &ImmediateWorker{Activities: env.activities, TenantStore: env.tenantStore}
	args := OffboardImmediateArgs{TenantID: tt.ID, TenantSlug: slug}

	// Call MarkOffboarding alone to simulate a crash before the remaining deletion work.
	if err := env.activities.MarkOffboarding(ctx, slug); err != nil {
		t.Fatalf("MarkOffboarding() error: %v", err)
	}

	// Retry: Work() must pick up from StatusOffboarding, not fail trying
	// to re-run MarkOffboarding.
	if err := w.Work(ctx, &river.Job[OffboardImmediateArgs]{Args: args}); err != nil {
		t.Fatalf("Work() on retry error: %v", err)
	}

	got, err := env.tenantStore.GetBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("GetBySlug() error: %v", err)
	}
	if got.Status != tenant.StatusDeleted {
		t.Errorf("Status = %q, want %q", got.Status, tenant.StatusDeleted)
	}

	if err := w.Work(ctx, &river.Job[OffboardImmediateArgs]{Args: args}); err != nil {
		t.Fatalf("Work() after completion error: %v", err)
	}
}

func TestImmediateWorker_UnexpectedStatusFails(t *testing.T) {
	env := newTestEnv(t, nil)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)
	ctx := t.Context()

	if _, err := env.tenantStore.UpdateStatus(ctx, slug, tenant.StatusSuspended, nil); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}

	w := &ImmediateWorker{Activities: env.activities, TenantStore: env.tenantStore}
	err := w.Work(ctx, &river.Job[OffboardImmediateArgs]{Args: OffboardImmediateArgs{TenantID: tt.ID, TenantSlug: slug}})
	if err == nil {
		t.Error("Work() on a suspended tenant: expected an error, got nil")
	}
}
