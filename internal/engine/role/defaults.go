package role

import (
	"context"
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// GrantModuleDefaults grants each permission in perms to the roles its
// DefaultRoles names, in the tenant's schema. It only adds missing grants,
// so existing ones, and roles the tenant has since edited, are left alone.
// A default role that does not exist in the tenant yet is skipped, as is a
// tenant whose RBAC tables are not created yet: a later call or the
// provisioning seed grants them once the roles exist.
func (s *Store) GrantModuleDefaults(ctx context.Context, tenantSlug string, perms []manifest.Permission) error {
	grant := fmt.Sprintf(`
		INSERT INTO %s.role_permissions (role_id, permission_name)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, tenantschema.Name(tenantSlug))

	roleIDs := map[string]string{}
	for _, perm := range perms {
		for _, roleName := range perm.DefaultRoles {
			roleID, seen := roleIDs[roleName]
			if !seen {
				var err error
				roleID, err = s.GetRoleByName(ctx, tenantSlug, roleName)
				switch {
				case errors.Is(err, ErrRoleNotFound):
				case isUndefinedTable(err):
					return nil
				case err != nil:
					return fmt.Errorf("resolve default role %q for permission %q: %w", roleName, perm.Name, err)
				}
				roleIDs[roleName] = roleID
			}
			if roleID == "" {
				continue
			}
			if _, err := s.db.ExecContext(ctx, grant, roleID, perm.Name); err != nil {
				return fmt.Errorf("grant %q to role %q: %w", perm.Name, roleName, err)
			}
		}
	}
	return nil
}
