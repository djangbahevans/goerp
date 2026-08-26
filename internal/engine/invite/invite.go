// Package invite is the tenant_invitations table from auth-internals.md
// §3 "Invite flow" — one physical copy per tenant, alongside roles/
// role_permissions/user_roles (internal/engine/role) in the same
// tenant_{slug} schema. Scoped to the operator-invite send/resend/revoke
// path goerp#31's `tenant create`/`tenant resend-invite` CLI commands
// need; the self-service accept-invite endpoint is separate, unfiled
// scope.
package invite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/rs/zerolog/log"
)

type Invitation struct {
	ID         string
	Email      string
	RoleID     string
	InvitedBy  *string
	ExpiresAt  time.Time
	AcceptedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

var ErrInvitationNotLive = errors.New("invitation not found or no longer live")

type Store struct {
	db     *sql.DB
	users  UserResolver
	roles  RoleResolver
	audit  AuditEmitter
	mailer Mailer
}

type UserResolver interface {
	FindOrCreateInvited(ctx context.Context, email string) (userID string, err error)
}

type RoleResolver interface {
	GetRoleByName(ctx context.Context, tenantSlug, name string) (roleID string, err error)
}

func NewStore(db *sql.DB, users UserResolver, roles RoleResolver, audit AuditEmitter, mailer Mailer) *Store {
	return &Store{db, users, roles, audit, mailer}
}

type AuditEmitter interface {
	Emit(ctx context.Context, tenantSlug, eventName string, payload map[string]any) error
}

type Mailer interface {
	SendInvite(ctx context.Context, email, tenantSlug, rawToken string, isNewUser bool) error
}

// Bootstrap creates tenant_invitations (and its partial index) in the
// given tenant's schema if they don't already exist. Does not create the
// schema itself, and does not create roles — assumes internal/engine/role's
// Bootstrap already ran against this tenant (tenant_invitations.role_id
// references {schema}.roles(id)). Concurrent-safe against other calls
// racing to bootstrap the same tenant's schema (goerp#171) via
// db.WithAdvisoryLock, scoped to tenantSlug the same way role.Store.
// Bootstrap is.
func (s *Store) Bootstrap(ctx context.Context, tenantSlug string) error {
	keys := []int64{db.AdvisoryLockKey("invite.Bootstrap:" + tenantSlug)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		schema := tenantschema.Name(tenantSlug)

		createTable := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.tenant_invitations (
			    id           UUID PRIMARY KEY DEFAULT uuidv7(),
			    email        TEXT NOT NULL,
			    role_id      UUID NOT NULL REFERENCES %s.roles(id) ON DELETE CASCADE,
			    invited_by   UUID,
			    token_hash   TEXT NOT NULL,
			    expires_at   TIMESTAMPTZ NOT NULL,
			    accepted_at  TIMESTAMPTZ,
			    revoked_at   TIMESTAMPTZ,
			    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)
		`, schema, schema)
		if _, err := tx.ExecContext(ctx, createTable); err != nil {
			return fmt.Errorf("create tenant_invitations table: %w", err)
		}

		createIndex := fmt.Sprintf(`
			CREATE UNIQUE INDEX IF NOT EXISTS tenant_invitations_email_pending_unique
			    ON %s.tenant_invitations(email) WHERE accepted_at IS NULL AND revoked_at IS NULL
		`, schema)
		if _, err := tx.ExecContext(ctx, createIndex); err != nil {
			return fmt.Errorf("create tenant_invitations email index: %w", err)
		}

		return nil
	})
}

const invitationColumns = `id, email, role_id, invited_by, expires_at, accepted_at, revoked_at, created_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInvitation(sc rowScanner) (*Invitation, error) {
	var inv Invitation
	var invitedBy sql.NullString
	if err := sc.Scan(
		&inv.ID, &inv.Email, &inv.RoleID, &invitedBy, &inv.ExpiresAt,
		&inv.AcceptedAt, &inv.RevokedAt, &inv.CreatedAt,
	); err != nil {
		return nil, err
	}
	if invitedBy.Valid {
		inv.InvitedBy = &invitedBy.String
	}
	return &inv, nil
}

// generateToken returns the raw token (hex, for the emailed link) and its
// SHA-256 hash (hex, what gets stored) — the raw token is never persisted.
func generateToken() (rawToken, tokenHash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate invite token: %w", err)
	}
	rawToken = fmt.Sprintf("%x", raw)
	sum := sha256.Sum256(raw)
	tokenHash = fmt.Sprintf("%x", sum)
	return rawToken, tokenHash, nil
}

// Invite resolves roleName to an id, finds-or-creates the invitee's
// system.users row, then upserts the invitation — reusing a live one for
// this email if it exists (auth-internals.md §3: sending to an
// already-invited email is equivalent to resend). Emits user.invited and
// sends the invite email; both are best-effort no-ops when audit/mailer
// are nil, logged rather than failing the invite itself.
func (s *Store) Invite(ctx context.Context, tenantSlug, email, roleName string, invitedBy *string) (*Invitation, error) {
	roleID, err := s.roles.GetRoleByName(ctx, tenantSlug, roleName)
	if err != nil {
		return nil, fmt.Errorf("resolve role %q: %w", roleName, err)
	}

	userID, err := s.users.FindOrCreateInvited(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("find or create invitee: %w", err)
	}

	rawToken, tokenHash, err := generateToken()
	if err != nil {
		return nil, err
	}

	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		INSERT INTO %s.tenant_invitations (email, role_id, invited_by, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, NOW() + INTERVAL '7 days')
		ON CONFLICT (email) WHERE accepted_at IS NULL AND revoked_at IS NULL
		DO UPDATE SET token_hash = EXCLUDED.token_hash, expires_at = EXCLUDED.expires_at
		RETURNING `+invitationColumns, schema)

	row := s.db.QueryRowContext(ctx, query, email, roleID, invitedBy, tokenHash)
	inv, err := scanInvitation(row)
	if err != nil {
		return nil, fmt.Errorf("upsert invitation: %w", err)
	}

	s.emit(ctx, tenantSlug, "user.invited", map[string]any{"invitation_id": inv.ID, "email": email})
	// userID is the invitee's system.users row — nothing here reads it
	// beyond confirming FindOrCreateInvited succeeded; whether the
	// invitee is new vs. existing (for email copy) isn't distinguished
	// yet since UserResolver only returns an id, not a found/created flag.
	s.sendInvite(ctx, email, tenantSlug, rawToken, true)
	_ = userID

	return inv, nil
}

// Resend rotates token_hash/expires_at on a still-live invitation and
// re-sends the email. Only valid while accepted_at IS NULL AND
// revoked_at IS NULL — ErrInvitationNotLive otherwise (already accepted,
// already revoked, or the id doesn't exist).
func (s *Store) Resend(ctx context.Context, tenantSlug, invitationID string) (*Invitation, error) {
	rawToken, tokenHash, err := generateToken()
	if err != nil {
		return nil, err
	}

	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		UPDATE %s.tenant_invitations
		SET token_hash = $1, expires_at = NOW() + INTERVAL '7 days'
		WHERE id = $2 AND accepted_at IS NULL AND revoked_at IS NULL
		RETURNING `+invitationColumns, schema)

	row := s.db.QueryRowContext(ctx, query, tokenHash, invitationID)
	inv, err := scanInvitation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvitationNotLive
		}
		return nil, fmt.Errorf("resend invitation: %w", err)
	}

	s.emit(ctx, tenantSlug, "user.invite_resent", map[string]any{"invitation_id": inv.ID})
	s.sendInvite(ctx, inv.Email, tenantSlug, rawToken, false)

	return inv, nil
}

// Revoke immediately invalidates a still-pending invitation's token and,
// since the partial unique index only constrains live rows, frees its
// email for a fresh invitation. Only valid while accepted_at IS NULL.
func (s *Store) Revoke(ctx context.Context, tenantSlug, invitationID string) error {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		UPDATE %s.tenant_invitations
		SET revoked_at = NOW()
		WHERE id = $1 AND accepted_at IS NULL AND revoked_at IS NULL
	`, schema)

	res, err := s.db.ExecContext(ctx, query, invitationID)
	if err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	if n == 0 {
		return ErrInvitationNotLive
	}

	s.emit(ctx, tenantSlug, "user.invite_revoked", map[string]any{"invitation_id": invitationID})

	return nil
}

// GetLiveByEmail returns the pending, unrevoked invitation for email, if
// any — what ResendInvite (the email-driven admin API path) resolves to
// an id with before calling Resend, since auth-internals.md's own
// resend/revoke operate on an invitation id, not an email.
func (s *Store) GetLiveByEmail(ctx context.Context, tenantSlug, email string) (*Invitation, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT `+invitationColumns+`
		FROM %s.tenant_invitations
		WHERE email = $1 AND accepted_at IS NULL AND revoked_at IS NULL
	`, schema)

	row := s.db.QueryRowContext(ctx, query, email)
	inv, err := scanInvitation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvitationNotLive
		}
		return nil, fmt.Errorf("get live invitation by email: %w", err)
	}

	return inv, nil
}

// ListExpired returns every still-live invitation (accepted_at IS NULL AND
// revoked_at IS NULL) whose expires_at has already passed, oldest first.
// Doesn't touch the row — expires_at > NOW() already excludes these from
// the accept flow (auth-internals.md §3); this is only for a caller that
// needs to notice the transition, e.g. goerp#163's expiry-audit job.
func (s *Store) ListExpired(ctx context.Context, tenantSlug string) ([]*Invitation, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT `+invitationColumns+`
		FROM %s.tenant_invitations
		WHERE expires_at < NOW() AND accepted_at IS NULL AND revoked_at IS NULL
		ORDER BY expires_at
	`, schema)

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list expired invitations: %w", err)
	}
	defer rows.Close()

	var invitations []*Invitation
	for rows.Next() {
		inv, err := scanInvitation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan expired invitation: %w", err)
		}
		invitations = append(invitations, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired invitations: %w", err)
	}

	return invitations, nil
}

// ResendInvite satisfies adminapi.InviteResender.
func (s *Store) ResendInvite(ctx context.Context, tenantSlug, email string) error {
	inv, err := s.GetLiveByEmail(ctx, tenantSlug, email)
	if err != nil {
		return err
	}
	_, err = s.Resend(ctx, tenantSlug, inv.ID)
	return err
}

func (s *Store) emit(ctx context.Context, tenantSlug, eventName string, payload map[string]any) {
	if s.audit == nil {
		log.Warn().Str("tenant", tenantSlug).Str("event", eventName).Msg("invite: no audit emitter wired, event not recorded")
		return
	}
	if err := s.audit.Emit(ctx, tenantSlug, eventName, payload); err != nil {
		log.Warn().Err(err).Str("tenant", tenantSlug).Str("event", eventName).Msg("invite: audit emit failed")
	}
}

func (s *Store) sendInvite(ctx context.Context, email, tenantSlug, rawToken string, isNewUser bool) {
	if s.mailer == nil {
		log.Warn().Str("tenant", tenantSlug).Str("email", email).Msg("invite: no mailer wired, invite email not sent")
		return
	}
	if err := s.mailer.SendInvite(ctx, email, tenantSlug, rawToken, isNewUser); err != nil {
		log.Warn().Err(err).Str("tenant", tenantSlug).Str("email", email).Msg("invite: send email failed")
	}
}
