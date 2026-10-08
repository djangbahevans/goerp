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

// InviteExpiryArgs runs hourly across active tenants and audit-logs expired invitations.
type InviteExpiryArgs struct{}

func (InviteExpiryArgs) Kind() string { return "invite_expiry" }

func (InviteExpiryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAdmin}
}

// InviteExpiryWorker audit-logs expired live invitations across active tenants.
// EventExists suppresses repeat notices because the expired invitation remains unchanged.
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
		if err := w.AuditStore.Emit(ctx, slug, "user.invite_expired", w.InviteStore.InviteeUserID(ctx, inv.Email), "", payload); err != nil {
			return fmt.Errorf("emit invite_expired for %s: %w", inv.ID, err)
		}
	}
	return nil
}
