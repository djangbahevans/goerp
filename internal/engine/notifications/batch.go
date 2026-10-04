package notifications

import (
	"context"
	"database/sql"
	"fmt"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// PreferencesForUsers loads every user's preferences in one database query.
// Bulk sends bypass the per-user cache to avoid a round trip per recipient
// and leave its generation-guarded entries untouched.
func (s *Store) PreferencesForUsers(ctx context.Context, tenantSlug, tenantID string, userIDs []string) (map[string]*Preferences, error) {
	prefs := make(map[string]*Preferences, len(userIDs))
	for _, id := range userIDs {
		if parsed, err := uuid.Parse(id); err == nil {
			id = parsed.String()
		}
		prefs[id] = &Preferences{Global: DefaultChannels, Types: map[string]Channels{}}
	}
	if len(userIDs) == 0 {
		return prefs, nil
	}

	query := fmt.Sprintf(`
		SELECT user_id, notification_type, email_enabled, sms_enabled, push_enabled
		FROM %s.notification_preferences
		WHERE tenant_id = $1 AND user_id = ANY($2::uuid[])
	`, tenantschema.Name(tenantSlug))
	rows, err := s.db.QueryContext(ctx, query, tenantID, userIDs)
	if err != nil {
		return nil, fmt.Errorf("load notification preferences: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var typ sql.NullString
		var channels Channels
		if err := rows.Scan(&id, &typ, &channels.Email, &channels.SMS, &channels.Push); err != nil {
			return nil, fmt.Errorf("load notification preferences: %w", err)
		}
		if typ.Valid {
			prefs[id].Types[typ.String] = channels
		} else {
			prefs[id].Global = channels
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load notification preferences: %w", err)
	}

	return prefs, nil
}

// DeviceTokensForUsers loads users' registered tokens in one query,
// preserving oldest-registration-first order within each user's tokens.
func (s *Store) DeviceTokensForUsers(ctx context.Context, tenantSlug, tenantID string, userIDs []string) (map[string][]DeviceToken, error) {
	tokens := make(map[string][]DeviceToken, len(userIDs))
	if len(userIDs) == 0 {
		return tokens, nil
	}

	query := fmt.Sprintf(`
		SELECT user_id, platform, token FROM %s.user_device_tokens
		WHERE tenant_id = $1 AND user_id = ANY($2::uuid[])
		ORDER BY user_id, registered_at, id
	`, tenantschema.Name(tenantSlug))
	rows, err := s.db.QueryContext(ctx, query, tenantID, userIDs)
	if err != nil {
		return nil, fmt.Errorf("list device tokens: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var token DeviceToken
		if err := rows.Scan(&id, &token.Platform, &token.Token); err != nil {
			return nil, fmt.Errorf("list device tokens: %w", err)
		}
		tokens[id] = append(tokens[id], token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list device tokens: %w", err)
	}

	return tokens, nil
}
