package jobqueue

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

// InviteExpiryArgs is the platform-wide periodic job that notices
// tenant_invitations rows past their expires_at and audit-logs the
// transition (goerp#163) — registered as an hourly river.PeriodicJob in
// New, not inserted by any caller. Never inserted per-tenant: a single
// run fans out across every active tenant itself, the same shape
// PartitionMaintenanceArgs uses for its own single-call-covers-everything
// platform-wide work.
type InviteExpiryArgs struct{}

func (InviteExpiryArgs) Kind() string { return "invite_expiry" }

func (InviteExpiryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAdmin}
}

// InviteExpiryWorker emits one user.invite_expired auth_audit_log event
// per still-live (never accepted or revoked) invitation whose expires_at
// has passed, across every active tenant. No UPDATE to the invitation row
// itself — expires_at > NOW() already excludes it from the accept flow
// (auth-internals.md §3); this only needs to notice and audit-log the
// transition. At-most-once per invitation is enforced by checking
// auth_audit_log itself before emitting (AuditStore.EventExists) rather
// than by any dedup window on the job/event insert, since the same
// still-expired, never-touched invitation would otherwise be
// rediscovered — and re-emitted for — on every future run indefinitely,
// not just a retry of this one run.
type InviteExpiryWorker struct {
	river.WorkerDefaults[InviteExpiryArgs]
	TenantStore *tenant.Store
	InviteStore *invite.Store
	AuditStore  *authaudit.Store
}

// Work enumerates active tenants and processes each independently,
// logging (not aborting on) a single tenant's failure — the same
// isolation tenantsync.SyncAll's own per-tenant fan-out already
// establishes, so one tenant with a transient DB issue doesn't block
// every other tenant's invitations from being noticed, this run or a
// retry of it.
func (w *InviteExpiryWorker) Work(ctx context.Context, job *river.Job[InviteExpiryArgs]) error {
	tenants, err := w.TenantStore.ActiveTenants(ctx)
	if err != nil {
		return fmt.Errorf("list active tenants: %w", err)
	}

	for _, t := range tenants {
		if err := w.expireForTenant(ctx, t.Slug); err != nil {
			log.Error().Err(err).Str("tenant", t.Slug).Msg("invite expiry: tenant failed")
		}
	}
	return nil
}

func (w *InviteExpiryWorker) expireForTenant(ctx context.Context, slug string) error {
	invitations, err := w.InviteStore.ListExpired(ctx, slug)
	if err != nil {
		return err
	}

	for _, inv := range invitations {
		exists, err := w.AuditStore.EventExists(ctx, "user.invite_expired", "invitation_id", inv.ID)
		if err != nil {
			return fmt.Errorf("check existing expiry event for %s: %w", inv.ID, err)
		}
		if exists {
			continue
		}

		payload := map[string]any{"invitation_id": inv.ID, "email": inv.Email}
		if err := w.AuditStore.Emit(ctx, slug, "user.invite_expired", payload); err != nil {
			return fmt.Errorf("emit invite_expired for %s: %w", inv.ID, err)
		}
	}
	return nil
}
