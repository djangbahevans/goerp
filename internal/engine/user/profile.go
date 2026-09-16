package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrProfileNotFound = errors.New("user profile not found")

type Profile struct {
	UserID       string
	Name         string
	AvatarFileID *string
	UpdatedAt    time.Time
}

// EnsureProfile creates userID's system.user_profiles row with name if one
// doesn't already exist. A no-op (doesn't overwrite name) when a row is
// already there — re-inviting an existing user (FindOrCreateInvited
// reusing their row) must not clobber a name they may have since set
// themselves, once a self-service rename path exists (goerp#819's own
// future scope).
func (s *Store) EnsureProfile(ctx context.Context, userID, name string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.user_profiles (user_id, name)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, name)
	if err != nil {
		return fmt.Errorf("ensure user profile: %w", err)
	}
	return nil
}

// ReplaceProfile is the self-service counterpart to EnsureProfile — it
// always overwrites name (an explicit user action, unlike an invite's
// seed value). avatarFileID has three states, since a PATCH body needs to
// express "leave it alone" separately from "the user removed their
// avatar":
//
//	nil                → avatar untouched
//	non-nil, ""         → avatar cleared (set NULL)
//	non-nil, "<file id>" → avatar set to that file
//
// Runs inside a transaction that locks any existing row with SELECT ...
// FOR UPDATE before reading it, so two concurrent calls replacing the
// same user's avatar can't both observe the same stale "previous avatar"
// value — each sees the other's write once it commits, so the returned
// oldAvatarFileID always reflects what this specific call actually
// replaced, letting the caller mark exactly that file (and no other) for
// cleanup. (A user's very first-ever profile save is the one case this
// can't cover — there's no existing row yet for FOR UPDATE to lock — but
// that has nothing to clean up regardless, since oldAvatarFileID is nil
// either way.) Creates the row if userID has none yet.
func (s *Store) ReplaceProfile(ctx context.Context, userID, name string, avatarFileID *string) (oldAvatarFileID *string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin replace profile: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var old sql.NullString
	row := tx.QueryRowContext(ctx, `SELECT avatar_file_id FROM system.user_profiles WHERE user_id = $1 FOR UPDATE`, userID)
	if err := row.Scan(&old); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lock existing profile: %w", err)
	}

	avatarProvided := avatarFileID != nil
	var newAvatarValue any
	if avatarProvided && *avatarFileID != "" {
		newAvatarValue = *avatarFileID
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO system.user_profiles (user_id, name, avatar_file_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			name = EXCLUDED.name,
			avatar_file_id = CASE WHEN $4 THEN EXCLUDED.avatar_file_id ELSE system.user_profiles.avatar_file_id END,
			updated_at = NOW()
	`, userID, name, newAvatarValue, avatarProvided); err != nil {
		return nil, fmt.Errorf("upsert profile: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit replace profile: %w", err)
	}

	if old.Valid {
		return &old.String, nil
	}
	return nil, nil
}

// GetProfile returns ErrProfileNotFound for a user created before
// user_profiles existed, or any user whose profile row was never created
// — callers (authme.Handler) treat that as "no display name/avatar yet"
// rather than an error condition of their own.
func (s *Store) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT user_id, name, avatar_file_id, updated_at
		FROM system.user_profiles
		WHERE user_id = $1
	`, userID)

	var p Profile
	if err := row.Scan(&p.UserID, &p.Name, &p.AvatarFileID, &p.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProfileNotFound
		}
		return nil, fmt.Errorf("get user profile: %w", err)
	}

	return &p, nil
}
