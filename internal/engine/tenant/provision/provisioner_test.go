package tenantprovision

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/adminapi"
	"github.com/djangbahevans/goerp/internal/engine/role"
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
		_ = tenantschema.Drop(context.Background(), env.conn, slug)
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
		_ = tenantschema.Drop(context.Background(), env.conn, slug)
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

func TestProvisionForRegistration_NilTemporalClientDoesNotPanic(t *testing.T) {
	p := NewProvisioner(nil, "goerp-system")
	if err := p.ProvisionForRegistration(t.Context(), "x", "X", "u"); err == nil {
		t.Error("ProvisionForRegistration() with a nil temporal client: expected an error, got nil")
	}
}

func TestProvisionForRegistration_GrantsTheExistingUserAdminWithoutAnInvite(t *testing.T) {
	slug := uniqueSlug(t)
	env := newTestEnv(t, nil)
	t.Cleanup(func() {
		_, _ = env.conn.Exec("DELETE FROM system.tenants WHERE slug = $1", slug)
		_ = tenantschema.Drop(context.Background(), env.conn, slug)
	})
	userID := uuid.New().String()

	p := NewProvisioner(env.temporalClient, env.taskQueue)
	if err := p.ProvisionForRegistration(t.Context(), slug, "Acme Corp", userID); err != nil {
		t.Fatalf("ProvisionForRegistration() error: %v", err)
	}

	tt, err := env.tenantStore.GetBySlug(t.Context(), slug)
	if err != nil {
		t.Fatalf("GetBySlug() error: %v", err)
	}
	if tt.Status != tenant.StatusActive {
		t.Errorf("Status = %q, want active", tt.Status)
	}
	roles, err := role.NewStore(env.conn).RoleNamesForUser(t.Context(), slug, userID)
	if err != nil {
		t.Fatalf("RoleNamesForUser() error: %v", err)
	}
	if len(roles) != 1 || roles[0] != "admin" {
		t.Errorf("roles = %v, want [admin]", roles)
	}
	var invites int
	if err := env.conn.QueryRow("SELECT count(*) FROM " + tenantschema.Name(slug) + ".tenant_invitations").Scan(&invites); err != nil {
		t.Fatalf("count invitations: %v", err)
	}
	if invites != 0 {
		t.Errorf("invitations = %d, want 0", invites)
	}
}

func TestProvisionForRegistration_TakenSlugFailsFast(t *testing.T) {
	slug := uniqueSlug(t)
	env := newTestEnv(t, nil)
	if _, err := env.tenantStore.CreateTenant(t.Context(), slug, "Existing Co"); err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = env.conn.Exec("DELETE FROM system.tenants WHERE slug = $1", slug) })

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	err := NewProvisioner(env.temporalClient, env.taskQueue).ProvisionForRegistration(ctx, slug, "Acme Corp", uuid.New().String())
	if !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("ProvisionForRegistration() error = %v, want ErrSlugTaken without retrying", err)
	}
}

// A task queue nobody polls holds the run open for as long as the test
// needs, so the wait deterministically outlasts its context.
func TestProvisionForRegistration_WaitOutlastingTheContextIsPending(t *testing.T) {
	slug := uniqueSlug(t)
	env := newTestEnv(t, nil)
	t.Cleanup(func() {
		_ = env.temporalClient.TerminateWorkflow(context.Background(), WorkflowID(slug), "", "test cleanup")
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := NewProvisioner(env.temporalClient, "unpolled-"+slug).ProvisionForRegistration(ctx, slug, "Slow Co", uuid.New().String())
	if !errors.Is(err, ErrProvisioningPending) {
		t.Fatalf("ProvisionForRegistration() error = %v, want ErrProvisioningPending", err)
	}
}
