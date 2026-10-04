package role

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// CheckMemberships reports users with an active member row and an unexpired
// role grant, matching IsMember. Only members appear in the returned map.
func (s *Store) CheckMemberships(ctx context.Context, tenantSlug string, userIDs []string) (map[string]bool, error) {
	members := make(map[string]bool, len(userIDs))
	if len(userIDs) == 0 {
		return members, nil
	}

	query := fmt.Sprintf(`
		SELECT DISTINCT ur.user_id
		FROM %[1]s.user_roles ur
		JOIN %[1]s.tenant_members tm ON tm.user_id = ur.user_id AND tm.status = 'active'
		WHERE ur.user_id = ANY($1::uuid[]) AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
	`, tenantschema.Name(tenantSlug))
	rows, err := s.db.QueryContext(ctx, query, userIDs)
	if err != nil {
		if isUndefinedTable(err) {
			return members, nil
		}
		return nil, fmt.Errorf("check tenant memberships: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("check tenant memberships: %w", err)
		}
		members[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("check tenant memberships: %w", err)
	}

	return members, nil
}
