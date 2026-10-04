package notify

import (
	"context"
	"fmt"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func (s *Sender) prepareRecipients(ctx context.Context, spec *sendSpec, userIDs []string, data map[string]any, opts Options) ([]*preparedSend, error) {
	users, err := s.loadRecipients(ctx, spec, userIDs)
	if err != nil {
		return nil, err
	}
	if err := s.loadRecipientTemplates(ctx, spec, users); err != nil {
		return nil, err
	}

	prepared := make([]*preparedSend, len(userIDs))
	for i, id := range userIDs {
		if prepared[i], err = s.prepareRecipient(ctx, spec, users[id], data, opts); err != nil {
			return nil, err
		}
	}

	return prepared, nil
}

func (s *Sender) loadRecipients(ctx context.Context, spec *sendSpec, userIDs []string) (map[string]*recipient, error) {
	for _, id := range userIDs {
		if _, err := uuid.Parse(id); err != nil {
			return nil, fmt.Errorf("%w: %q", ErrUnknownUser, id)
		}
	}

	t := spec.tenant
	members, err := s.Members.CheckMemberships(ctx, t.Slug, userIDs)
	if err != nil {
		return nil, fmt.Errorf("check notification recipient membership: %w", err)
	}
	for _, id := range userIDs {
		if !members[id] {
			return nil, fmt.Errorf("%w: %q", ErrUnknownUser, id)
		}
	}

	// Phone numbers belong to the receiving tenant's membership, so a
	// notification cannot disclose a number supplied to another tenant.
	query := fmt.Sprintf(`
		SELECT u.id, u.email, COALESCE(p.name, ''), COALESCE(p.locale, ''), COALESCE(tm.phone, '')
		FROM system.users u
		LEFT JOIN system.user_profiles p ON p.user_id = u.id
		LEFT JOIN %s.tenant_members tm ON tm.user_id = u.id
		WHERE u.id = ANY($1::uuid[]) AND u.deleted_at IS NULL
	`, tenantschema.Name(t.Slug))
	rows, err := s.DB.QueryContext(ctx, query, userIDs)
	if err != nil {
		return nil, fmt.Errorf("load notification recipients: %w", err)
	}
	defer rows.Close()

	users := make(map[string]*recipient, len(userIDs))
	for rows.Next() {
		u := &recipient{}
		if err := rows.Scan(&u.id, &u.email, &u.name, &u.locale, &u.phone); err != nil {
			return nil, fmt.Errorf("load notification recipients: %w", err)
		}
		users[u.id] = u
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load notification recipients: %w", err)
	}
	for _, id := range userIDs {
		if users[id] == nil {
			return nil, fmt.Errorf("%w: %q", ErrUnknownUser, id)
		}
	}

	var prefs map[string]*notifications.Preferences
	if len(userIDs) == 1 {
		p, err := s.Store.Preferences(ctx, t.Slug, t.ID, userIDs[0])
		if err != nil {
			return nil, err
		}
		prefs = map[string]*notifications.Preferences{userIDs[0]: p}
	} else {
		prefs, err = s.Store.PreferencesForUsers(ctx, t.Slug, t.ID, userIDs)
		if err != nil {
			return nil, err
		}
	}
	tokens, err := s.Store.DeviceTokensForUsers(ctx, t.Slug, t.ID, userIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range userIDs {
		users[id].prefs = prefs[id]
		users[id].tokens = tokens[id]
	}

	return users, nil
}

func (s *Sender) loadRecipientTemplates(ctx context.Context, spec *sendSpec, users map[string]*recipient) error {
	locales := make(map[string]bool, len(users))
	for _, u := range users {
		locales[u.locale] = true
	}

	templates, err := loadSendTemplatesForLocales(ctx, s.Store, spec.tenant.Slug, spec.notificationType, locales)
	if err != nil {
		return err
	}
	spec.templates = templates

	return nil
}

// recipient contains the per-user data needed to route and render a send.
type recipient struct {
	id     string
	email  string
	name   string
	locale string
	phone  string
	prefs  *notifications.Preferences
	tokens []notifications.DeviceToken
}
