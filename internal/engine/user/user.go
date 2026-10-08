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
    status                 TEXT NOT NULL DEFAULT 'active'
                               CHECK (status IN ('active','invited','suspended','pending_verification','deleted')),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at             TIMESTAMPTZ,
    locked_until           TIMESTAMPTZ,
    locked_by              TEXT,
    failed_login_count     INTEGER NOT NULL DEFAULT 0
)
`

const createIndex = `
    CREATE UNIQUE INDEX IF NOT EXISTS users_email_active_unique_idx
        ON system.users (email) WHERE deleted_at IS NULL;
`

// Profiles persist avatar file IDs because signed URLs expire and must be generated per
// request. NULL locale preferences inherit tenant defaults.
const createUserProfilesTable = `
CREATE TABLE IF NOT EXISTS system.user_profiles (
    user_id         UUID PRIMARY KEY REFERENCES system.users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    avatar_file_id  UUID,
    locale          TEXT,
    timezone        TEXT,
    theme           TEXT NOT NULL DEFAULT 'system'
                        CHECK (theme IN ('light', 'dark', 'system')),
    contrast        TEXT NOT NULL DEFAULT 'system'
                        CHECK (contrast IN ('standard', 'high', 'system')),
    date_format     TEXT
                        CHECK (date_format IN ('day_first', 'month_first', 'iso')),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`

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
	ErrUserNotFound       = errors.New("user not found")
	ErrResetTokenInvalid  = errors.New("password reset token invalid or expired")
	ErrVerifyTokenInvalid = errors.New("email verify token invalid or expired")
)

type User struct {
	ID               string
	Email            string
	Status           Status
	PasswordHash     *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	LockedUntil      *time.Time
	FailedLoginCount int
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db}
}

// Bootstrap creates user tables after the system schema exists. An advisory lock
// serializes concurrent callers.
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

		return nil
	})
}

const userColumns = `id, email, status, password_hash, created_at, updated_at, locked_until, failed_login_count`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(sc rowScanner) (*User, error) {
	var u User
	if err := sc.Scan(&u.ID, &u.Email, &u.Status, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt, &u.LockedUntil, &u.FailedLoginCount); err != nil {
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

// ResetLoginState clears id's failure counter and lock — auth-internals.md
// §3 step 11's "Reset brute force counter". The last sign-in is per tenant,
// recorded on the member row (role.Store.RecordLogin).
func (s *Store) ResetLoginState(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.users
		SET failed_login_count = 0,
		    locked_until = NULL
		WHERE id = $1
	`, id)
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

// ConsumePasswordResetToken sets the new password hash, clears the
// token, and lifts any login lockout in one conditional UPDATE, so of two
// concurrent confirms with the same token exactly one succeeds. Never
// touches status.
func (s *Store) ConsumePasswordResetToken(ctx context.Context, tokenHash, passwordHash string) (string, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE system.users
		SET password_hash = $2,
		    password_reset_token = NULL,
		    password_reset_expiry = NULL,
		    failed_login_count = 0,
		    locked_until = NULL,
		    updated_at = NOW()
		WHERE password_reset_token = $1 AND password_reset_expiry > NOW() AND deleted_at IS NULL
		RETURNING id
	`, tokenHash, passwordHash)

	var id string
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrResetTokenInvalid
		}
		return "", fmt.Errorf("consume password reset token: %w", err)
	}

	return id, nil
}

// SetPassword stores a new password hash — auth-internals.md §3
// "Password change". Never touches status or the login lockout.
func (s *Store) SetPassword(ctx context.Context, id, passwordHash string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE system.users
		SET password_hash = $2,
		    updated_at = NOW()
		WHERE id = $1
	`, id, passwordHash)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// ErrNotActivatable reports that ActivateWithPasswordTx found the user no
// longer an invited account without a password.
var ErrNotActivatable = errors.New("user is no longer an invited account without a password")

// ActivateWithPasswordTx sets a first password and activates the account
// inside tx — auth-internals.md §3 "Invite acceptance" step 4. Only an
// invited user with no password yet is touched, so this can never
// reactivate a suspended account.
func (s *Store) ActivateWithPasswordTx(ctx context.Context, tx *sql.Tx, id, passwordHash string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE system.users
		SET password_hash = $2,
		    status = 'active',
		    updated_at = NOW()
		WHERE id = $1 AND password_hash IS NULL AND status = 'invited'
	`, id, passwordHash)
	if err != nil {
		return fmt.Errorf("activate user with password: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotActivatable
	}
	return nil
}

// ErrEmailTaken reports that an active account already uses the email.
var ErrEmailTaken = errors.New("email already registered")

// CreateRegistered creates a self-registered account with its password
// already set (auth-internals.md §3 "Self-service registration"), in
// status active or pending_verification — never invited. Its password was
// validated against the global policy only, since the tenant it founds
// doesn't exist yet.
func (s *Store) CreateRegistered(ctx context.Context, email, passwordHash string, status Status) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO system.users (email, password_hash, status)
		VALUES ($1, $2, $3)
		ON CONFLICT (email) WHERE deleted_at IS NULL DO NOTHING
		RETURNING id
	`, strings.ToLower(email), passwordHash, status).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrEmailTaken
	}
	if err != nil {
		return "", fmt.Errorf("create registered user: %w", err)
	}
	return id, nil
}

// DeleteRegistered removes an account CreateRegistered just made, when
// the registration that created it fails before the tenant exists.
func (s *Store) DeleteRegistered(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM system.users WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete registered user: %w", err)
	}
	return nil
}

// SetEmailVerifyToken stores tokenHash as id's pending email-verification
// token — a column pair separate from the password reset token's.
func (s *Store) SetEmailVerifyToken(ctx context.Context, id, tokenHash string, expiry time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.users
		SET email_verify_token = $2, email_verify_expiry = $3, updated_at = NOW()
		WHERE id = $1
	`, id, tokenHash, expiry)
	if err != nil {
		return fmt.Errorf("set email verify token: %w", err)
	}
	return nil
}

// ConsumeEmailVerifyToken marks the email verified, clears the token, and
// activates a pending_verification account in one conditional UPDATE, so
// of two concurrent confirms with the same token exactly one succeeds.
// Never reads the password reset token.
func (s *Store) ConsumeEmailVerifyToken(ctx context.Context, tokenHash string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE system.users
		SET email_verified = TRUE,
		    email_verify_token = NULL,
		    email_verify_expiry = NULL,
		    status = CASE WHEN status = 'pending_verification' THEN 'active' ELSE status END,
		    updated_at = NOW()
		WHERE email_verify_token = $1 AND email_verify_expiry > NOW() AND deleted_at IS NULL
		RETURNING `+userColumns+`
	`, tokenHash)

	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrVerifyTokenInvalid
		}
		return nil, fmt.Errorf("consume email verify token: %w", err)
	}

	return u, nil
}
