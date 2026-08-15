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
    contact_id             UUID,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at             TIMESTAMPTZ,
    last_login_at          TIMESTAMPTZ,
    last_login_ip          INET,
    locked_until           TIMESTAMPTZ,
    locked_by              TEXT
)
`

const createIndex = `
    CREATE UNIQUE INDEX IF NOT EXISTS users_email_active_unique_idx
        ON system.users (email) WHERE deleted_at IS NULL;
`

type Status string

const (
	StatusActive              Status = "active"
	StatusInvited             Status = "invited"
	StatusSuspended           Status = "suspended"
	StatusPendingVerification Status = "pending_verification"
	StatusDeleted             Status = "deleted"
)

var ErrUserNotFound = errors.New("user not found")

type User struct {
	ID           string
	Email        string
	Status       Status
	PasswordHash *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
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

		return nil
	})
}

const userColumns = `id, email, status, password_hash, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(sc rowScanner) (*User, error) {
	var u User
	if err := sc.Scan(&u.ID, &u.Email, &u.Status, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt); err != nil {
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
