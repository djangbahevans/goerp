package tenantoffboard

import (
	"context"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/riverqueue/river"
)

// TestImmediateWorker_RetryAfterPartialCompletionIsIdempotent guards the
// property job.go's own doc comment describes: River re-invokes Work from
// scratch on retry (unlike a Temporal workflow, which never re-runs a
// completed activity). A first Work() call that gets the tenant to
// StatusOffboarding, followed by a second Work() call on the same args
// (simulating a retry after a crash partway through), must finish the job
// rather than failing on MarkOffboarding's own CAS (scoped to
// status = 'active', which is no longer true the second time).
func TestImmediateWorker_RetryAfterPartialCompletionIsIdempotent(t *testing.T) {
	env := newTestEnv(t, nil)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)
	ctx := context.Background()

	w := &ImmediateWorker{Activities: env.activities, TenantStore: env.tenantStore}
	args := OffboardImmediateArgs{TenantID: tt.ID, TenantSlug: slug}

	// First call: only get as far as MarkOffboarding, simulating a crash
	// right after — call the activity directly rather than the full
	// Work(), so the tenant is left in StatusOffboarding without the rest
	// having run.
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

	// A second retry, now that the tenant is already StatusDeleted, must
	// also be a no-op rather than an error.
	if err := w.Work(ctx, &river.Job[OffboardImmediateArgs]{Args: args}); err != nil {
		t.Fatalf("Work() after completion error: %v", err)
	}
}

func TestImmediateWorker_UnexpectedStatusFails(t *testing.T) {
	env := newTestEnv(t, nil)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)
	ctx := context.Background()

	if _, err := env.tenantStore.UpdateStatus(ctx, slug, tenant.StatusSuspended, nil); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}

	w := &ImmediateWorker{Activities: env.activities, TenantStore: env.tenantStore}
	err := w.Work(ctx, &river.Job[OffboardImmediateArgs]{Args: OffboardImmediateArgs{TenantID: tt.ID, TenantSlug: slug}})
	if err == nil {
		t.Error("Work() on a suspended tenant: expected an error, got nil")
	}
}
