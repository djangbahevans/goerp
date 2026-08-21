package tenantprovision

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/adminapi"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// TestStartProvisioning_NilTemporalClientDoesNotPanic guards against
// Engine.New's temporalClient field being nil (Temporal unreachable at
// startup, warn-only — not fail-hard for the engine as a whole) reaching
// this Provisioner and panicking on first use instead of failing
// cleanly. Reproduced first (a bare ExecuteWorkflow call on a nil
// *temporal.Client panics inside the SDK), then fixed.
func TestStartProvisioning_NilTemporalClientDoesNotPanic(t *testing.T) {
	p := NewProvisioner(nil, "goerp-system")

	_, err := p.StartProvisioning(context.Background(), adminapi.CreateTenantRequest{
		Slug:       "x",
		AdminEmail: "x@example.com",
	})
	if err == nil {
		t.Error("StartProvisioning() with a nil temporal client: expected an error, got nil")
	}
}

func TestStartProvisioning_ProvisionsTenant(t *testing.T) {
	slug := uniqueSlug(t)
	env := newTestEnv(t, nil)
	t.Cleanup(func() {
		_, _ = env.conn.Exec("DELETE FROM system.tenants WHERE slug = $1", slug)
		_, _ = env.conn.Exec("DROP SCHEMA IF EXISTS " + tenantschema.Name(slug) + " CASCADE")
	})

	p := NewProvisioner(env.temporalClient, env.taskQueue)

	workflowID, err := p.StartProvisioning(context.Background(), adminapi.CreateTenantRequest{
		Slug:       slug,
		Name:       "Acme Corp",
		AdminEmail: slug + "@example.com",
	})
	if err != nil {
		t.Fatalf("StartProvisioning() error: %v", err)
	}
	if workflowID != WorkflowID(slug) {
		t.Errorf("workflowID = %q, want %q", workflowID, WorkflowID(slug))
	}

	// Give the workflow time to actually run to completion, then confirm
	// it really did — StartProvisioning itself only starts it.
	// ErrTenantNotFound is expected on early iterations, before the
	// ReserveSlug activity has run yet — only a different error, or
	// running out of time, is a real failure.
	deadline := time.Now().Add(20 * time.Second)
	for {
		tt, err := env.tenantStore.GetBySlug(context.Background(), slug)
		switch {
		case err == nil && tt.Status == tenant.StatusActive:
			return
		case err != nil && !errors.Is(err, tenant.ErrTenantNotFound):
			t.Fatalf("GetBySlug() error: %v", err)
		case time.Now().After(deadline):
			t.Fatalf("tenant not active after 20s (last GetBySlug: tenant=%+v, err=%v)", tt, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestStartProvisioning_RetryReplaysSameWorkflowID is the concrete
// behavior cli-reference.md §5's "retrying a tenant create call ...
// replays the original {slug, workflow_id} rather than starting a second
// provisioning workflow" describes — verified against the real Temporal
// server (client.StartWorkflowOptions.WorkflowExecutionErrorWhenAlreadyStarted
// defaults to false, so a duplicate start returns the existing run rather
// than erroring), not assumed from the SDK doc comment alone.
func TestStartProvisioning_RetryReplaysSameWorkflowID(t *testing.T) {
	slug := uniqueSlug(t)
	env := newTestEnv(t, nil)
	t.Cleanup(func() {
		_, _ = env.conn.Exec("DELETE FROM system.tenants WHERE slug = $1", slug)
		_, _ = env.conn.Exec("DROP SCHEMA IF EXISTS " + tenantschema.Name(slug) + " CASCADE")
	})

	p := NewProvisioner(env.temporalClient, env.taskQueue)
	req := adminapi.CreateTenantRequest{Slug: slug, Name: "Acme Corp", AdminEmail: slug + "@example.com"}

	first, err := p.StartProvisioning(context.Background(), req)
	if err != nil {
		t.Fatalf("first StartProvisioning() error: %v", err)
	}

	second, err := p.StartProvisioning(context.Background(), req)
	if err != nil {
		t.Fatalf("second (retry) StartProvisioning() error: %v", err)
	}

	if first != second {
		t.Errorf("workflow_id changed on retry: first = %q, second = %q", first, second)
	}
}
