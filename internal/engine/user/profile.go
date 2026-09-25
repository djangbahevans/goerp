package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrProfileNotFound = errors.New("user profile not found")

// Theme and DateFormat values system.user_profiles accepts
// (auth-internals.md §2).
var (
	Themes      = []string{"light", "dark", "system"}
	DateFormats = []string{"day_first", "month_first", "iso"}
)

type Profile struct {
	UserID       string
	Name         string
	AvatarFileID *string
	Theme        string
	// Nil inherits the tenant default.
	Locale     *string
	Timezone   *string
	DateFormat *string
	UpdatedAt  time.Time
}

// DisplayName is the profile's name, or nil when it holds UpdateProfile's
// "" placeholder, which callers report the same as having no profile.
func (p *Profile) DisplayName() *string {
	if p.Name == "" {
		return nil
	}
	return &p.Name
}

// NullableField is a PATCH field that can be absent (Set false, left
// untouched), null (Set true, Value nil, reset to inherit) or a value.
type NullableField struct {
	Set   bool
	Value *string
}

// ProfileUpdate holds the fields a self-service profile save changes; a
// nil or unset field is left as it is. AvatarFileID has three states:
//
//	nil                  → avatar untouched
//	non-nil, ""          → avatar cleared (set NULL)
//	non-nil, "<file id>" → avatar set to that file
type ProfileUpdate struct {
	Name         *string
	AvatarFileID *string
	Theme        *string
	Locale       NullableField
	Timezone     NullableField
	DateFormat   NullableField
}

// EnsureProfile creates userID's system.user_profiles row with name if one
// doesn't already exist. An existing row keeps its name — re-inviting an
// existing user (FindOrCreateInvited reusing their row) must not clobber a
// name they may have set themselves — unless it holds UpdateProfile's ""
// placeholder, which name replaces.
func (s *Store) EnsureProfile(ctx context.Context, userID, name string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.user_profiles (user_id, name)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET name = EXCLUDED.name, updated_at = NOW()
			WHERE system.user_profiles.name = ''
	`, userID, name)
	if err != nil {
		return fmt.Errorf("ensure user profile: %w", err)
	}
	return nil
}

// UpdateProfile is the self-service counterpart to EnsureProfile: it
// changes only the fields update sets, and creates the row if userID has
// none yet. A row created without a name stores "" as a placeholder:
// DisplayName reports it as no name, and EnsureProfile fills it in.
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
// either way.)
func (s *Store) UpdateProfile(ctx context.Context, userID string, update ProfileUpdate) (oldAvatarFileID *string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin update profile: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var old sql.NullString
	row := tx.QueryRowContext(ctx, `SELECT avatar_file_id FROM system.user_profiles WHERE user_id = $1 FOR UPDATE`, userID)
	if err := row.Scan(&old); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lock existing profile: %w", err)
	}

	var newAvatarValue any
	if update.AvatarFileID != nil && *update.AvatarFileID != "" {
		newAvatarValue = *update.AvatarFileID
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO system.user_profiles (user_id, name, avatar_file_id, theme, locale, timezone, date_format)
		VALUES ($1, COALESCE($2, ''), $3, COALESCE($4, 'system'), $5, $6, $7)
		ON CONFLICT (user_id) DO UPDATE SET
			name = CASE WHEN $2::text IS NULL THEN system.user_profiles.name ELSE EXCLUDED.name END,
			avatar_file_id = CASE WHEN $8 THEN EXCLUDED.avatar_file_id ELSE system.user_profiles.avatar_file_id END,
			theme = CASE WHEN $4::text IS NULL THEN system.user_profiles.theme ELSE EXCLUDED.theme END,
			locale = CASE WHEN $9 THEN EXCLUDED.locale ELSE system.user_profiles.locale END,
			timezone = CASE WHEN $10 THEN EXCLUDED.timezone ELSE system.user_profiles.timezone END,
			date_format = CASE WHEN $11 THEN EXCLUDED.date_format ELSE system.user_profiles.date_format END,
			updated_at = NOW()
	`, userID, update.Name, newAvatarValue, update.Theme,
		update.Locale.Value, update.Timezone.Value, update.DateFormat.Value,
		update.AvatarFileID != nil, update.Locale.Set, update.Timezone.Set, update.DateFormat.Set); err != nil {
		return nil, fmt.Errorf("upsert profile: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit update profile: %w", err)
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
		SELECT user_id, name, avatar_file_id, theme, locale, timezone, date_format, updated_at
		FROM system.user_profiles
		WHERE user_id = $1
	`, userID)

	var p Profile
	if err := row.Scan(&p.UserID, &p.Name, &p.AvatarFileID, &p.Theme, &p.Locale, &p.Timezone, &p.DateFormat, &p.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProfileNotFound
		}
		return nil, fmt.Errorf("get user profile: %w", err)
	}

	return &p, nil
}
