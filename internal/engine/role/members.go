package role

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

const createTenantMembers = `
CREATE TABLE IF NOT EXISTS %s.tenant_members (
    user_id         UUID PRIMARY KEY,
    status          TEXT NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'suspended')),
    suspended_at    TIMESTAMPTZ,
    suspended_by    UUID,
    suspend_reason  TEXT,
    mfa_reset_at    TIMESTAMPTZ,
    contact_id      UUID,
    phone           TEXT,
    job_title       TEXT,
    last_login_at   TIMESTAMPTZ,
    last_login_ip   INET,
    joined_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`

const createMembershipIndex = `
CREATE TABLE IF NOT EXISTS system.tenant_memberships (
    user_id    UUID NOT NULL REFERENCES system.users(id) ON DELETE CASCADE,
    tenant_id  UUID NOT NULL REFERENCES system.tenants(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, tenant_id)
);
CREATE INDEX IF NOT EXISTS tenant_memberships_tenant_idx ON system.tenant_memberships (tenant_id);
`

// Unregistered tenant schemas and harnesses without an index produce no
// membership entry. Definer execution uses the canonical bootstrap owner.
const createSyncFunction = `
CREATE OR REPLACE FUNCTION system.sync_tenant_membership() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    tid uuid;
BEGIN
    IF to_regclass('system.tenant_memberships') IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT id INTO tid FROM system.tenants WHERE 'tenant_' || slug = TG_TABLE_SCHEMA;
    IF tid IS NULL THEN
        RETURN NULL;
    END IF;
    IF TG_OP = 'INSERT' THEN
        INSERT INTO system.tenant_memberships (user_id, tenant_id)
        VALUES (NEW.user_id, tid) ON CONFLICT DO NOTHING;
    ELSE
        DELETE FROM system.tenant_memberships WHERE user_id = OLD.user_id AND tenant_id = tid;
    END IF;
    RETURN NULL;
END
$$
`

const attachSyncTrigger = `
CREATE OR REPLACE TRIGGER sync_tenant_membership
    AFTER INSERT OR DELETE ON %s.tenant_members
    FOR EACH ROW EXECUTE FUNCTION system.sync_tenant_membership()
`

var syncFunctionLockKey = db.AdvisoryLockKey("role.sync_tenant_membership")

const membershipOwner = "schema_sync_user"

func validateMembershipOwners(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT 'system.tenant_memberships', pg_get_userbyid(relowner)
		FROM pg_class WHERE oid = to_regclass('system.tenant_memberships')
		UNION ALL
		SELECT 'system.sync_tenant_membership()', pg_get_userbyid(proowner)
		FROM pg_proc WHERE oid = to_regprocedure('system.sync_tenant_membership()')
	`)
	if err != nil {
		return fmt.Errorf("look up membership bootstrap ownership: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var object, owner string
		if err := rows.Scan(&object, &owner); err != nil {
			return fmt.Errorf("scan membership bootstrap ownership: %w", err)
		}

		if owner != membershipOwner {
			return fmt.Errorf("membership bootstrap: %s is owned by %s; expected %s; initialize a fresh database with docker/postgres-initdb/database/setup.sql or coordinate ownership repair with the database administrator", object, owner, membershipOwner)
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("read membership bootstrap ownership: %w", err)
	}

	return nil
}

func asMembershipOwner(ctx context.Context, tx *sql.Tx, fn func() error) error {
	var caller string
	var superuser bool
	if err := tx.QueryRowContext(ctx, `SELECT current_user, rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&caller, &superuser); err != nil {
		return fmt.Errorf("look up membership bootstrap role: %w", err)
	}

	if caller != membershipOwner && !superuser {
		return fmt.Errorf("membership bootstrap requires %s through the schema-sync pool; connected as %s", membershipOwner, caller)
	}

	// Superuser fixtures create the same definer identity as production.
	// SET LOCAL prevents a pooled connection from retaining that identity.
	if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE schema_sync_user`); err != nil {
		return fmt.Errorf("select membership bootstrap role: %w", err)
	}

	if err := fn(); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `SELECT set_config('role', $1, true)`, caller); err != nil {
		return fmt.Errorf("restore membership bootstrap caller: %w", err)
	}

	return nil
}

func ensureSyncFunction(ctx context.Context, tx *sql.Tx) error {
	return asMembershipOwner(ctx, tx, func() error {
		if _, err := tx.ExecContext(ctx, createSyncFunction); err != nil {
			return fmt.Errorf("create membership sync function: %w", err)
		}

		return nil
	})
}

func (s *Store) BootstrapMembershipIndex(ctx context.Context) error {
	return db.WithAdvisoryLock(ctx, s.db, []int64{syncFunctionLockKey}, func(tx *sql.Tx) error {
		if err := validateMembershipOwners(ctx, tx); err != nil {
			return err
		}

		return asMembershipOwner(ctx, tx, func() error {
			if _, err := tx.ExecContext(ctx, createMembershipIndex); err != nil {
				return fmt.Errorf("create tenant_memberships table: %w", err)
			}

			if _, err := tx.ExecContext(ctx, createSyncFunction); err != nil {
				return fmt.Errorf("create membership sync function: %w", err)
			}

			return nil
		})
	})
}

func (s *Store) AttachMembershipTrigger(ctx context.Context, tenantSlug string) error {
	keys := []int64{syncFunctionLockKey, db.AdvisoryLockKey("role.Bootstrap:" + tenantSlug)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := validateMembershipOwners(ctx, tx); err != nil {
			return err
		}

		if err := ensureSyncFunction(ctx, tx); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, fmt.Sprintf(attachSyncTrigger, tenantschema.Name(tenantSlug))); err != nil {
			return fmt.Errorf("attach membership sync trigger: %w", err)
		}

		return nil
	})
}

// ReindexMemberships inserts an index row for every tenant_members row of
// the tenant — the bulk path tenant import takes once its restore
// finishes. Idempotent.
func (s *Store) ReindexMemberships(ctx context.Context, tenantID, tenantSlug string) error {
	query := fmt.Sprintf(`
		INSERT INTO system.tenant_memberships (user_id, tenant_id)
		SELECT tm.user_id, $1 FROM %s.tenant_members tm
		JOIN system.users u ON u.id = tm.user_id
		ON CONFLICT DO NOTHING
	`, tenantschema.Name(tenantSlug))
	if _, err := s.db.ExecContext(ctx, query, tenantID); err != nil {
		return fmt.Errorf("reindex tenant memberships: %w", err)
	}
	return nil
}

// CandidateTenantIDs returns the tenants the membership index lists for
// userID. The index knows nothing of suspension or role expiry, so a
// caller confirms each with IsMember.
func (s *Store) CandidateTenantIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tenant_id FROM system.tenant_memberships WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("list candidate tenants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan candidate tenant: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list candidate tenants: %w", err)
	}
	return ids, nil
}

// MemberStatus is tenant_members.status.
type MemberStatus string

const (
	MemberActive    MemberStatus = "active"
	MemberSuspended MemberStatus = "suspended"
)

// ErrNotMember is returned when userID has no tenant_members row.
var ErrNotMember = errors.New("not a member of this tenant")

// AddMemberTx creates userID's member row, a no-op when one exists — the
// membership-creation point of invite acceptance, SSO provisioning and
// tenant provisioning (auth-internals.md §2 "Tenant members").
func AddMemberTx(ctx context.Context, q db.Execer, tenantSlug, userID string) error {
	query := fmt.Sprintf(`INSERT INTO %s.tenant_members (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, tenantschema.Name(tenantSlug))
	if _, err := q.ExecContext(ctx, query, userID); err != nil {
		return fmt.Errorf("add tenant member: %w", err)
	}
	return nil
}

// AddMember is AddMemberTx outside a transaction.
func (s *Store) AddMember(ctx context.Context, tenantSlug, userID string) error {
	return AddMemberTx(ctx, s.db, tenantSlug, userID)
}

// HasMemberRow reports whether userID has a tenant_members row, active or
// suspended.
func (s *Store) HasMemberRow(ctx context.Context, tenantSlug, userID string) (bool, error) {
	query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.tenant_members WHERE user_id = $1)`, tenantschema.Name(tenantSlug))
	var ok bool
	if err := s.db.QueryRowContext(ctx, query, userID).Scan(&ok); err != nil {
		if isUndefinedTable(err) {
			return false, nil
		}
		return false, fmt.Errorf("check member row: %w", err)
	}
	return ok, nil
}

// JoinedAt returns when userID's tenant_members row was created, or
// ErrNotMember.
func (s *Store) JoinedAt(ctx context.Context, tenantSlug, userID string) (time.Time, error) {
	query := fmt.Sprintf(`SELECT joined_at FROM %s.tenant_members WHERE user_id = $1`, tenantschema.Name(tenantSlug))
	var joinedAt time.Time
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&joinedAt)
	if errors.Is(err, sql.ErrNoRows) || isUndefinedTable(err) {
		return time.Time{}, ErrNotMember
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("get member join time: %w", err)
	}
	return joinedAt, nil
}

// MemberProfile is the per-tenant part of a person's profile.
type MemberProfile struct {
	ContactID *string
	Phone     *string
	JobTitle  *string
}

// GetMemberProfile returns userID's per-tenant profile fields, or
// ErrNotMember.
func (s *Store) GetMemberProfile(ctx context.Context, tenantSlug, userID string) (MemberProfile, error) {
	query := fmt.Sprintf(`SELECT contact_id::text, phone, job_title FROM %s.tenant_members WHERE user_id = $1`, tenantschema.Name(tenantSlug))
	var p MemberProfile
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&p.ContactID, &p.Phone, &p.JobTitle)
	if errors.Is(err, sql.ErrNoRows) || isUndefinedTable(err) {
		return MemberProfile{}, ErrNotMember
	}
	if err != nil {
		return MemberProfile{}, fmt.Errorf("get member profile: %w", err)
	}
	return p, nil
}

// SetMemberContact sets or clears ("") userID's linked contact.
func (s *Store) SetMemberContact(ctx context.Context, tenantSlug, userID, contactID string) error {
	query := fmt.Sprintf(`UPDATE %s.tenant_members SET contact_id = NULLIF($2, '')::uuid WHERE user_id = $1`, tenantschema.Name(tenantSlug))
	res, err := s.db.ExecContext(ctx, query, userID, contactID)
	if err != nil {
		return fmt.Errorf("set member contact: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotMember
	}
	return nil
}

// RecordLogin stores a session issued for the tenant as userID's last
// sign-in there. ip stores as NULL when empty; a non-member is a no-op.
func (s *Store) RecordLogin(ctx context.Context, tenantSlug, userID, ip string) error {
	query := fmt.Sprintf(`UPDATE %s.tenant_members SET last_login_at = NOW(), last_login_ip = NULLIF($2, '')::inet WHERE user_id = $1`, tenantschema.Name(tenantSlug))
	if _, err := s.db.ExecContext(ctx, query, userID, ip); err != nil {
		return fmt.Errorf("record member login: %w", err)
	}
	return nil
}

// Member lifecycle errors.
var (
	ErrMemberStateChanged = errors.New("member status does not allow this transition")
	ErrLastAdmin          = errors.New("the tenant's last active admin")
)

// SuspendMemberTx suspends an active member inside tx.
func SuspendMemberTx(ctx context.Context, tx *sql.Tx, tenantSlug, userID, suspendedBy, reason string) error {
	query := fmt.Sprintf(`
		UPDATE %s.tenant_members
		SET status = 'suspended', suspended_at = $2, suspended_by = NULLIF($3, '')::uuid, suspend_reason = $4
		WHERE user_id = $1 AND status = 'active'
	`, tenantschema.Name(tenantSlug))
	return execTransition(ctx, tx, query, userID, time.Now(), suspendedBy, reason)
}

// UnsuspendMemberTx returns a suspended member to active inside tx.
func UnsuspendMemberTx(ctx context.Context, tx *sql.Tx, tenantSlug, userID string) error {
	query := fmt.Sprintf(`
		UPDATE %s.tenant_members
		SET status = 'active', suspended_at = NULL, suspended_by = NULL, suspend_reason = NULL
		WHERE user_id = $1 AND status = 'suspended'
	`, tenantschema.Name(tenantSlug))
	return execTransition(ctx, tx, query, userID)
}

// RemoveMemberTx deletes userID's role grants and member row inside tx;
// the trigger drops the index row.
func RemoveMemberTx(ctx context.Context, tx *sql.Tx, tenantSlug, userID string) error {
	schema := tenantschema.Name(tenantSlug)
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s.user_roles WHERE user_id = $1`, schema), userID); err != nil {
		return fmt.Errorf("remove member roles: %w", err)
	}
	return execTransition(ctx, tx, fmt.Sprintf(`DELETE FROM %s.tenant_members WHERE user_id = $1`, schema), userID)
}

func execTransition(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("change member status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("change member status: %w", err)
	}
	if n == 0 {
		return ErrMemberStateChanged
	}
	return nil
}

// activeAdminsQuery selects the tenant's active admins: an unexpired
// admin grant, an active member row and an active account.
const activeAdminsQuery = `
	SELECT ur.user_id
	FROM %[1]s.user_roles ur
	JOIN %[1]s.roles r ON r.id = ur.role_id AND r.name = 'admin'
	JOIN %[1]s.tenant_members tm ON tm.user_id = ur.user_id AND tm.status = 'active'
	JOIN system.users u ON u.id = ur.user_id AND u.status = 'active' AND u.deleted_at IS NULL
	WHERE ur.expires_at IS NULL OR ur.expires_at > NOW()
`

// GuardLastAdminTx returns ErrLastAdmin when userID is an active admin of
// the tenant and no other active admin remains. It locks the admin role's
// grants first, so two concurrent calls removing different admins can't
// both pass.
func GuardLastAdminTx(ctx context.Context, tx *sql.Tx, tenantSlug, userID string) error {
	schema := tenantschema.Name(tenantSlug)
	lock := fmt.Sprintf(`SELECT id FROM %s.roles WHERE name = 'admin' FOR UPDATE`, schema)
	if _, err := tx.ExecContext(ctx, lock); err != nil {
		return fmt.Errorf("lock admin role: %w", err)
	}
	var isAdmin bool
	var others int
	query := fmt.Sprintf(`
		WITH admins AS (`+activeAdminsQuery+`)
		SELECT EXISTS (SELECT 1 FROM admins WHERE user_id = $1),
		       (SELECT COUNT(DISTINCT user_id) FROM admins WHERE user_id <> $1)
	`, schema)
	if err := tx.QueryRowContext(ctx, query, userID).Scan(&isAdmin, &others); err != nil {
		return fmt.Errorf("count active admins: %w", err)
	}
	if isAdmin && others == 0 {
		return ErrLastAdmin
	}
	return nil
}

// MemberProfileUpdate changes the per-tenant profile fields whose Set flag
// is true; a nil value clears the field.
type MemberProfileUpdate struct {
	SetPhone    bool
	Phone       *string
	SetJobTitle bool
	JobTitle    *string
}

// UpdateMemberProfile applies update to userID's member row, or returns
// ErrNotMember.
func (s *Store) UpdateMemberProfile(ctx context.Context, tenantSlug, userID string, update MemberProfileUpdate) error {
	if !update.SetPhone && !update.SetJobTitle {
		return nil
	}
	query := fmt.Sprintf(`
		UPDATE %s.tenant_members
		SET phone = CASE WHEN $2 THEN $3 ELSE phone END,
		    job_title = CASE WHEN $4 THEN $5 ELSE job_title END
		WHERE user_id = $1
	`, tenantschema.Name(tenantSlug))
	res, err := s.db.ExecContext(ctx, query, userID, update.SetPhone, update.Phone, update.SetJobTitle, update.JobTitle)
	if err != nil {
		return fmt.Errorf("update member profile: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotMember
	}
	return nil
}

// RevokeAdminRole is RevokeRole for the tenant's admin role (roleID),
// refused with ErrLastAdmin when userID is the tenant's last active admin.
func (s *Store) RevokeAdminRole(ctx context.Context, tenantSlug, userID, roleID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin revoke admin role: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := GuardLastAdminTx(ctx, tx, tenantSlug, userID); err != nil {
		return err
	}
	query := fmt.Sprintf(`DELETE FROM %s.user_roles WHERE user_id = $1 AND role_id = $2`, tenantschema.Name(tenantSlug))
	if _, err := tx.ExecContext(ctx, query, userID, roleID); err != nil {
		return fmt.Errorf("revoke role: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit revoke admin role: %w", err)
	}
	return nil
}
