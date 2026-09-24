package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

const createUsersTable = `
CREATE TABLE IF NOT EXISTS system.users (
    id                     UUID PRIMARY KEY DEFAULT uuidv7(),
    email                  TEXT NOT NULL,
    email_verified         BOOLEAN NOT NULL DEFAULT FALSE,
    email_verify_token     TEXT,
    email_verify_expiry    TIMESTAMPTZ,
    password_reset_token   TEXT,
    password_reset_expiry  TIMESTAMPTZ,
    password_hash          TEXT,
    password_set_at_policy_version BIGINT NOT NULL DEFAULT 0,
    password_set_at_policy_tenant_id UUID,
    status                 TEXT NOT NULL DEFAULT 'active'
                               CHECK (status IN ('active','invited','suspended','pending_verification','deleted')),
    contact_id             UUID,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at             TIMESTAMPTZ,
    last_login_at          TIMESTAMPTZ,
    last_login_ip          INET,
    locked_until           TIMESTAMPTZ,
    locked_by              TEXT,
    failed_login_count     INTEGER NOT NULL DEFAULT 0
)
`

const createIndex = `
    CREATE UNIQUE INDEX IF NOT EXISTS users_email_active_unique_idx
        ON system.users (email) WHERE deleted_at IS NULL;
`

// createUserProfilesTable matches auth-internals.md's user_profiles
// definition, minus locale/timezone/phone — those have no consumer yet
// (separate, unfiled tickets), so they're left off rather than added
// unused; a later ticket can add them with ALTER TABLE the same way
// tenant.Tenant's suspended_by comment describes for its own deferred
// column. avatar_file_id (goerp#819) stores a files.id, not a URL —
// storage.SignedURL expires (max 24h), so a resolvable URL is generated
// fresh on every GET /auth/me rather than persisted here.
const createUserProfilesTable = `
CREATE TABLE IF NOT EXISTS system.user_profiles (
    user_id         UUID PRIMARY KEY REFERENCES system.users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    avatar_file_id  UUID,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`

// migrateAvatarURLColumn renames a pre-goerp#819 deployment's
// avatar_url TEXT (goerp#817's original column name, live on main before
// this ticket) to avatar_file_id UUID. CREATE TABLE IF NOT EXISTS above
// is a no-op against an already-bootstrapped table, so without this an
// already-running environment (the shared dev Postgres included — hit
// firsthand while building this ticket) keeps the old column forever and
// every GetProfile/SetProfile call starts failing with "column
// avatar_file_id does not exist". No shipped code path ever wrote a real
// value to avatar_url, but the USING clause still guards the cast with a
// UUID-shape check rather than casting unconditionally: an environment
// where something wrote non-UUID data into the column out of band (a
// manual edit, a one-off script) gets that value silently dropped to
// NULL instead of failing the cast — and since this runs inside
// Bootstrap's transaction, a cast failure here would otherwise fail the
// entire engine startup, not just the profile feature.
const migrateAvatarURLColumn = `
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'system' AND table_name = 'user_profiles' AND column_name = 'avatar_url'
    ) THEN
        ALTER TABLE system.user_profiles RENAME COLUMN avatar_url TO avatar_file_id;
        ALTER TABLE system.user_profiles ALTER COLUMN avatar_file_id TYPE UUID USING (
            CASE
                WHEN avatar_file_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                THEN avatar_file_id::uuid
                ELSE NULL
            END
        );
    END IF;
END $$;
`

// failedLoginLockThreshold/lockDuration are the minimal single-tier
// lockout auth-internals.md §3 step 5/§15's login flow requires
// ("brute force counters ... reject if locked"). The full escalating
// policy (doubling duration on repeated lockouts within 24h, a security
// notification email, an audit log entry, admin manual-unlock) is
// backlog #291 ("Account lockout after repeated failures"), unfiled and
// explicitly out of scope here — this is enough for a login attempt to
// actually be gated on repeated failures, not the complete policy.
const (
	failedLoginLockThreshold = 10
	lockDuration             = 30 * time.Minute
)

type Status string

const (
	StatusActive              Status = "active"
	StatusInvited             Status = "invited"
	StatusSuspended           Status = "suspended"
	StatusPendingVerification Status = "pending_verification"
	StatusDeleted             Status = "deleted"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrResetTokenInvalid = errors.New("password reset token invalid or expired")
)

type User struct {
	ID               string
	Email            string
	Status           Status
	PasswordHash     *string
	ContactID        *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	LockedUntil      *time.Time
	FailedLoginCount int
	// PasswordSetAtPolicyTenantID/Version identify the tenant policy the
	// password was last validated against (auth-internals.md §3 "Password
	// policy versioning"); a nil tenant means the global policy only.
	PasswordSetAtPolicyTenantID *string
	PasswordSetAtPolicyVersion  int64
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db}
}

// Bootstrap creates system.users (and its index) if it doesn't already
// exist. Relies on the system schema already existing — created by
// whichever of tenant.Store/schema.SchemaSyncPool/auditlog.Store's own
// Bootstrap engine.go calls first, not by this package. Concurrent-safe
// against other processes calling Bootstrap at the same time (goerp#171)
// via db.WithAdvisoryLock.
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.AdvisoryLockKey("user.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, createUsersTable); err != nil {
			return fmt.Errorf("create users table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createIndex); err != nil {
			return fmt.Errorf("create users email index: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createUserProfilesTable); err != nil {
			return fmt.Errorf("create user_profiles table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, migrateAvatarURLColumn); err != nil {
			return fmt.Errorf("migrate user_profiles avatar column: %w", err)
		}

		return nil
	})
}

const userColumns = `id, email, status, password_hash, contact_id::text, created_at, updated_at, locked_until, failed_login_count, password_set_at_policy_tenant_id::text, password_set_at_policy_version`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(sc rowScanner) (*User, error) {
	var u User
	if err := sc.Scan(&u.ID, &u.Email, &u.Status, &u.PasswordHash, &u.ContactID, &u.CreatedAt, &u.UpdatedAt, &u.LockedUntil, &u.FailedLoginCount, &u.PasswordSetAtPolicyTenantID, &u.PasswordSetAtPolicyVersion); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByEmail looks up the active (non-deleted) row for email, normalised
// to lowercase before comparison — auth-internals.md §2 "Email uniqueness"
// never stores or compares raw-case email.
func (s *Store) GetByEmail(ctx context.Context, email string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		FROM system.users
		WHERE email = $1 AND deleted_at IS NULL
	`, strings.ToLower(email))

	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}

	return u, nil
}

func (s *Store) GetByID(ctx context.Context, id string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		FROM system.users
		WHERE id = $1
	`, id)

	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user by id: %w", err)
	}

	return u, nil
}

// FindOrCreateInvited returns the id of the active (non-deleted) row for
// email if one exists — whatever its status; an already-active user
// invited to a second tenant stays untouched, per auth-internals.md §3
// "Invite flow" — or creates one with status='invited' and no password.
// One round trip, race-safe: the ON CONFLICT DO UPDATE is a true no-op
// (sets updated_at to itself) that exists only to make RETURNING fire on
// conflict, so two concurrent invites for the same brand-new email both
// resolve to the same row instead of one erroring.
func (s *Store) FindOrCreateInvited(ctx context.Context, email string) (string, error) {
	normalised := strings.ToLower(email)

	row := s.db.QueryRowContext(ctx, `
		INSERT INTO system.users (email, status)
		VALUES ($1, $2)
		ON CONFLICT (email) WHERE deleted_at IS NULL
		DO UPDATE SET updated_at = system.users.updated_at
		RETURNING id
	`, normalised, StatusInvited)

	var id string
	if err := row.Scan(&id); err != nil {
		return "", fmt.Errorf("find or create invited user: %w", err)
	}

	return id, nil
}

// IncrementFailedLogins increments id's failed_login_count and, once it
// reaches failedLoginLockThreshold, sets locked_until lockDuration from
// now. The lock timestamp is computed in Go and bound as a plain
// parameter — a Postgres interval literal built from Go's own
// Duration.String() ("30m0s") isn't valid interval syntax, so this
// avoids that class of mistake entirely rather than working around it in
// SQL. A single UPDATE, so a concurrent increment for the same id can't
// read a stale count and under-count the threshold check.
func (s *Store) IncrementFailedLogins(ctx context.Context, id string) error {
	lockUntil := time.Now().Add(lockDuration)
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.users
		SET failed_login_count = failed_login_count + 1,
		    locked_until = CASE
		        WHEN failed_login_count + 1 >= $2 THEN $3
		        ELSE locked_until
		    END
		WHERE id = $1
	`, id, failedLoginLockThreshold, lockUntil)
	if err != nil {
		return fmt.Errorf("increment failed logins: %w", err)
	}
	return nil
}

// ResetLoginState clears id's failure counter and lock, and records a
// successful login's timestamp/IP — auth-internals.md §3 step 11's
// "Reset brute force counter" and "Update last_login_at and
// last_login_ip", done together since both only ever happen on the same
// successful-login path. ip stores as SQL NULL when empty.
func (s *Store) ResetLoginState(ctx context.Context, id, ip string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.users
		SET failed_login_count = 0,
		    locked_until = NULL,
		    last_login_at = NOW(),
		    last_login_ip = NULLIF($2, '')::inet
		WHERE id = $1
	`, id, ip)
	if err != nil {
		return fmt.Errorf("reset login state: %w", err)
	}
	return nil
}

// UpdatePasswordHash overwrites id's stored hash — auth-internals.md §3
// step 9's transparent re-hash when a verified password's stored hash
// used outdated Argon2id params.
func (s *Store) UpdatePasswordHash(ctx context.Context, id, hash string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.users SET password_hash = $2, updated_at = NOW() WHERE id = $1
	`, id, hash)
	if err != nil {
		return fmt.Errorf("update password hash: %w", err)
	}
	return nil
}

// SetPasswordResetToken stores tokenHash as id's single pending reset
// token, replacing any earlier one.
func (s *Store) SetPasswordResetToken(ctx context.Context, id, tokenHash string, expiry time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.users
		SET password_reset_token = $2, password_reset_expiry = $3, updated_at = NOW()
		WHERE id = $1
	`, id, tokenHash, expiry)
	if err != nil {
		return fmt.Errorf("set password reset token: %w", err)
	}
	return nil
}

func (s *Store) GetByPasswordResetToken(ctx context.Context, tokenHash string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		FROM system.users
		WHERE password_reset_token = $1 AND password_reset_expiry > NOW() AND deleted_at IS NULL
	`, tokenHash)

	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrResetTokenInvalid
		}
		return nil, fmt.Errorf("get user by password reset token: %w", err)
	}

	return u, nil
}

// ConsumePasswordResetToken sets the new password hash and the tenant
// policy it was validated against (policyTenantID "" for the global
// policy), clears the token, and lifts any login lockout in one
// conditional UPDATE, so of two concurrent confirms with the same token
// exactly one succeeds. Never touches status.
func (s *Store) ConsumePasswordResetToken(ctx context.Context, tokenHash, passwordHash, policyTenantID string, policyVersion int64) (string, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE system.users
		SET password_hash = $2,
		    password_set_at_policy_tenant_id = NULLIF($3, '')::uuid,
		    password_set_at_policy_version = $4,
		    password_reset_token = NULL,
		    password_reset_expiry = NULL,
		    failed_login_count = 0,
		    locked_until = NULL,
		    updated_at = NOW()
		WHERE password_reset_token = $1 AND password_reset_expiry > NOW() AND deleted_at IS NULL
		RETURNING id
	`, tokenHash, passwordHash, policyTenantID, policyVersion)

	var id string
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrResetTokenInvalid
		}
		return "", fmt.Errorf("consume password reset token: %w", err)
	}

	return id, nil
}
