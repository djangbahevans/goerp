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
	UserID    string
	Name      string
	AvatarURL *string
	UpdatedAt time.Time
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

// GetProfile returns ErrProfileNotFound for a user created before
// user_profiles existed, or any user whose profile row was never created
// — callers (authme.Handler) treat that as "no display name/avatar yet"
// rather than an error condition of their own.
func (s *Store) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT user_id, name, avatar_url, updated_at
		FROM system.user_profiles
		WHERE user_id = $1
	`, userID)

	var p Profile
	if err := row.Scan(&p.UserID, &p.Name, &p.AvatarURL, &p.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProfileNotFound
		}
		return nil, fmt.Errorf("get user profile: %w", err)
	}

	return &p, nil
}
