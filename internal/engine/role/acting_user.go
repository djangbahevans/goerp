package role

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type ActingUser struct {
	ContactID string
	Roles     []string
}

// ResolveActingUser reads the tenant's contact link and unexpired role grants
// from one database snapshot. An empty user ID represents an anonymous caller.
func (s *Store) ResolveActingUser(ctx context.Context, tenantSlug, userID string) (ActingUser, error) {
	if userID == "" {
		return ActingUser{}, nil
	}
	query := fmt.Sprintf(`
		SELECT COALESCE(tm.contact_id::text, ''), r.name
		FROM %[1]s.tenant_members tm
		LEFT JOIN %[1]s.user_roles ur ON ur.user_id = tm.user_id
			AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
		LEFT JOIN %[1]s.roles r ON r.id = ur.role_id
		WHERE tm.user_id = $1 AND tm.status = 'active'
		ORDER BY r.name
	`, tenantschema.Name(tenantSlug))
	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return ActingUser{}, fmt.Errorf("resolve acting user: %w", err)
	}
	defer func() { _ = rows.Close() }()
	actor := ActingUser{Roles: []string{}}
	found := false
	for rows.Next() {
		var name sql.NullString
		if err := rows.Scan(&actor.ContactID, &name); err != nil {
			return ActingUser{}, fmt.Errorf("scan acting user: %w", err)
		}
		found = true
		if name.Valid {
			actor.Roles = append(actor.Roles, name.String)
		}
	}
	if err := rows.Err(); err != nil {
		return ActingUser{}, fmt.Errorf("read acting user: %w", err)
	}
	if !found {
		return ActingUser{}, ErrNotMember
	}
	return actor, nil
}
