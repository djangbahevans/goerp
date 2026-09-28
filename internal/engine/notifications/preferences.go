package notifications

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/rs/zerolog/log"
)

// PreferencesTable is the channel-preference table's unqualified name.
const PreferencesTable = "notification_preferences"

// Channel names, as the preference routes spell them. in_app has no
// preference: it is always delivered.
const (
	ChannelInApp = "in_app"
	ChannelEmail = "email"
	ChannelSMS   = "sms"
	ChannelPush  = "push"
)

// preferencesCacheTTL is how long a user's cached preferences live
// (notification-system.md §8 "Preference cache").
const preferencesCacheTTL = 15 * time.Minute

// Channels is one set of per-channel switches: a user's global defaults,
// or their override for one notification type.
type Channels struct {
	Email bool `json:"email"`
	SMS   bool `json:"sms"`
	Push  bool `json:"push"`
}

// DefaultChannels are the column defaults, which apply to a user with no
// global row.
var DefaultChannels = Channels{Email: true, SMS: false, Push: true}

// ChannelsPatch is a partial Channels; a nil field is left unchanged.
type ChannelsPatch struct {
	Email *bool `json:"email,omitzero"`
	SMS   *bool `json:"sms,omitzero"`
	Push  *bool `json:"push,omitzero"`
}

func (p ChannelsPatch) apply(c Channels) Channels {
	if p.Email != nil {
		c.Email = *p.Email
	}
	if p.SMS != nil {
		c.SMS = *p.SMS
	}
	if p.Push != nil {
		c.Push = *p.Push
	}
	return c
}

// Preferences is every stored preference of one user: Global (the NULL
// notification_type row, or DefaultChannels without one) and each
// per-type row, keyed by notification type.
type Preferences struct {
	Global Channels            `json:"global"`
	Types  map[string]Channels `json:"types"`
}

// WithCache makes s read preferences through c and invalidate them there
// on writes. Returns s.
func (s *Store) WithCache(c *cache.Client) *Store {
	s.cache = c
	return s
}

func preferencesCacheKey(tenantID, userID string) string {
	return tenantID + ":notif_prefs:" + userID
}

// The preference cache entry is a Redis hash: prefsDataField holds the
// encoded Preferences ("" when not cached), prefsGenField a generation id
// that every write replaces. A reader caches what it loaded only while the
// generation it started with is still current, so a read that raced a
// write can't put the pre-write preferences back.
const (
	prefsGenField  = "gen"
	prefsDataField = "data"
)

// Preferences returns userID's preferences, from the cache when it holds
// them. A cache failure falls back to the database.
func (s *Store) Preferences(ctx context.Context, tenantSlug, tenantID, userID string) (*Preferences, error) {
	if s.cache == nil {
		return loadPreferences(ctx, s.db, tenantSlug, tenantID, userID)
	}

	key := preferencesCacheKey(tenantID, userID)
	gen := ""
	fields, found, err := s.cache.GetHash(ctx, key)
	if err == nil {
		if data := fields[prefsDataField]; found && data != "" {
			var p Preferences
			if err := json.Unmarshal([]byte(data), &p); err == nil {
				return &p, nil
			}
		}
		gen = fields[prefsGenField]
		if gen == "" {
			// Start a generation before reading, so a write that commits
			// after the read below replaces it.
			gen = uuid.New().String()
			if _, err := s.cache.CompareAndSetHash(ctx, key, prefsGenField, false, false, "", prefsDataField, "", gen, preferencesCacheTTL); err != nil {
				gen = ""
			}
		}
	}

	p, err := loadPreferences(ctx, s.db, tenantSlug, tenantID, userID)
	if err != nil {
		return nil, err
	}
	if s.afterLoad != nil {
		s.afterLoad()
	}
	if gen != "" {
		if encoded, err := json.Marshal(p); err == nil {
			_, _ = s.cache.CompareAndSetHash(ctx, key, prefsGenField, true, true, gen, prefsDataField, string(encoded), gen, preferencesCacheTTL)
		}
	}
	return p, nil
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func loadPreferences(ctx context.Context, q queryer, tenantSlug, tenantID, userID string) (*Preferences, error) {
	query := fmt.Sprintf(`
		SELECT notification_type, email_enabled, sms_enabled, push_enabled
		FROM %s.notification_preferences
		WHERE tenant_id = $1 AND user_id = $2
	`, tenantschema.Name(tenantSlug))

	rows, err := q.QueryContext(ctx, query, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("load notification preferences: %w", err)
	}
	defer rows.Close()

	p := &Preferences{Global: DefaultChannels, Types: map[string]Channels{}}
	for rows.Next() {
		var typ sql.NullString
		var c Channels
		if err := rows.Scan(&typ, &c.Email, &c.SMS, &c.Push); err != nil {
			return nil, fmt.Errorf("load notification preferences: %w", err)
		}
		if typ.Valid {
			p.Types[typ.String] = c
		} else {
			p.Global = c
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load notification preferences: %w", err)
	}
	return p, nil
}

// UpdatePreferences applies global, when non-nil, to userID's global row,
// then each of types to that type's row, creating rows as needed. A new
// type row takes any channel its patch leaves unset from the (updated)
// global row. A type whose patched settings equal the global row has its
// row removed instead, so it follows global again. Invalidates the user's
// cached preferences.
func (s *Store) UpdatePreferences(ctx context.Context, tenantSlug, tenantID, userID string, global *ChannelsPatch, types map[string]ChannelsPatch) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("update notification preferences: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := loadPreferences(ctx, tx, tenantSlug, tenantID, userID)
	if err != nil {
		return err
	}
	upsert := fmt.Sprintf(`
		INSERT INTO %s.notification_preferences (tenant_id, user_id, notification_type, email_enabled, sms_enabled, push_enabled)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, user_id, notification_type) DO UPDATE
		SET email_enabled = EXCLUDED.email_enabled,
		    sms_enabled   = EXCLUDED.sms_enabled,
		    push_enabled  = EXCLUDED.push_enabled,
		    updated_at    = NOW()
	`, tenantschema.Name(tenantSlug))
	deleteType := fmt.Sprintf(`
		DELETE FROM %s.notification_preferences
		WHERE tenant_id = $1 AND user_id = $2 AND notification_type = $3
	`, tenantschema.Name(tenantSlug))

	if global != nil {
		current.Global = global.apply(current.Global)
		if _, err := tx.ExecContext(ctx, upsert, tenantID, userID, nil, current.Global.Email, current.Global.SMS, current.Global.Push); err != nil {
			return fmt.Errorf("update global notification preferences: %w", err)
		}
	}
	for typ, patch := range types {
		base, ok := current.Types[typ]
		if !ok {
			base = current.Global
		}
		c := patch.apply(base)
		if c == current.Global {
			if _, err := tx.ExecContext(ctx, deleteType, tenantID, userID, typ); err != nil {
				return fmt.Errorf("reset %s notification preferences: %w", typ, err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, upsert, tenantID, userID, typ, c.Email, c.SMS, c.Push); err != nil {
			return fmt.Errorf("update %s notification preferences: %w", typ, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update notification preferences: %w", err)
	}

	s.invalidatePreferences(ctx, tenantID, userID)
	return nil
}

func (s *Store) invalidatePreferences(ctx context.Context, tenantID, userID string) {
	if s.cache == nil {
		return
	}
	_, err := s.cache.CompareAndSetHash(ctx, preferencesCacheKey(tenantID, userID), prefsGenField, false, false, "", prefsDataField, "", uuid.New().String(), preferencesCacheTTL)
	if err != nil {
		log.Warn().Err(err).Str("tenant_id", tenantID).Str("user_id", userID).Msg("notification preferences: cache invalidation failed")
	}
}

// AvailableChannels returns the channels operationally active for
// tenantID (notification-system.md §8): in_app and email always, plus sms
// and push when an enabled module row carries that channel's provider
// category in system.tenant_module_settings.
func (s *Store) AvailableChannels(ctx context.Context, tenantID string) ([]string, error) {
	var sms, push bool
	err := s.db.QueryRowContext(ctx, `
		SELECT
		    COALESCE(bool_or(provider_category = 'sms_provider'), false),
		    COALESCE(bool_or(provider_category = 'push_provider'), false)
		FROM system.tenant_module_settings
		WHERE tenant_id = $1 AND enabled
	`, tenantID).Scan(&sms, &push)
	if err != nil {
		return nil, fmt.Errorf("load available notification channels: %w", err)
	}

	channels := []string{ChannelInApp, ChannelEmail}
	if sms {
		channels = append(channels, ChannelSMS)
	}
	if push {
		channels = append(channels, ChannelPush)
	}
	return channels, nil
}

// BootstrapPreferences creates notification_preferences in the given
// tenant's schema if it doesn't already exist, the same way BootstrapFeed
// does notifications. The unique constraint treats NULLs as equal, so a
// user has at most one global (NULL notification_type) row and upserts
// on it conflict.
func (s *Store) BootstrapPreferences(ctx context.Context, tenantSlug string) error {
	schema := tenantschema.Name(tenantSlug)
	return s.bootstrap(ctx, "notifications.BootstrapPreferences:"+tenantSlug, []string{
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.notification_preferences (
			    id                 UUID PRIMARY KEY DEFAULT uuidv7(),
			    tenant_id          UUID NOT NULL,
			    user_id            UUID NOT NULL,
			    notification_type  TEXT,
			    email_enabled      BOOLEAN NOT NULL DEFAULT true,
			    sms_enabled        BOOLEAN NOT NULL DEFAULT false,
			    push_enabled       BOOLEAN NOT NULL DEFAULT true,
			    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    UNIQUE NULLS NOT DISTINCT (tenant_id, user_id, notification_type)
			)
		`, schema),
	})
}
