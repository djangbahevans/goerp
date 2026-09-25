package role

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

var (
	ErrRoleImmutable = errors.New("role is immutable")
	ErrRoleNameTaken = errors.New("role name already exists")
	ErrRoleInUse     = errors.New("role is still assigned to users")
)

// Summary is one role as the tenant admin roles pages show it. UserCount
// counts live grants and InvitationCount pending invitations, each only for
// accounts that aren't deleted, the same people the user directory lists.
type Summary struct {
	ID              string
	Name            string
	Description     *string
	IsImmutable     bool
	UserCount       int
	InvitationCount int
}

// Detail adds the role's own granted permission names, sorted.
type Detail struct {
	Summary
	Permissions []string
}

// Change is a partial update: a nil field is left as it is, and an empty
// Description clears it.
type Change struct {
	Name        *string
	Description *string
	Permissions *[]string
}

// InTx runs inside a role write's transaction before it commits, for the
// caller's audit row and change notification.
type InTx func(tx *sql.Tx) error

const (
	liveGrant         = `(ur.expires_at IS NULL OR ur.expires_at > NOW())`
	pendingInvitation = `(ti.accepted_at IS NULL AND ti.revoked_at IS NULL)`
	// A deleted account keeps its grants and invitations, but no longer
	// holds or is offered the role.
	heldByAccount    = liveGrant + ` AND EXISTS (SELECT 1 FROM system.users u WHERE u.id = ur.user_id AND u.deleted_at IS NULL)`
	offeredToAccount = pendingInvitation + ` AND EXISTS (SELECT 1 FROM system.users u WHERE u.email = ti.email AND u.deleted_at IS NULL)`
)

func summaryQuery(schema string) string {
	return fmt.Sprintf(`
		SELECT r.id, r.name, r.description, r.is_immutable,
		       (SELECT COUNT(*) FROM %[1]s.user_roles ur WHERE ur.role_id = r.id AND `+heldByAccount+`),
		       (SELECT COUNT(*) FROM %[1]s.tenant_invitations ti WHERE ti.role_id = r.id AND `+offeredToAccount+`)
		FROM %[1]s.roles r`, schema)
}

func scanSummary(sc interface{ Scan(dest ...any) error }) (Summary, error) {
	var s Summary
	err := sc.Scan(&s.ID, &s.Name, &s.Description, &s.IsImmutable, &s.UserCount, &s.InvitationCount)
	return s, err
}

// ListRoles returns every role in the tenant, built-in roles first, then by
// name.
func (s *Store) ListRoles(ctx context.Context, tenantSlug string) ([]Summary, error) {
	rows, err := s.db.QueryContext(ctx, summaryQuery(tenantschema.Name(tenantSlug))+` ORDER BY r.is_immutable DESC, r.name`)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []Summary{}
	for rows.Next() {
		summary, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		out = append(out, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate roles: %w", err)
	}
	return out, nil
}

// GetRole returns roleID with its permissions, or ErrRoleNotFound.
func (s *Store) GetRole(ctx context.Context, tenantSlug, roleID string) (Detail, error) {
	schema := tenantschema.Name(tenantSlug)
	summary, err := scanSummary(s.db.QueryRowContext(ctx, summaryQuery(schema)+` WHERE r.id = $1`, roleID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Detail{}, ErrRoleNotFound
		}
		return Detail{}, fmt.Errorf("get role: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT permission_name FROM %s.role_permissions WHERE role_id = $1 ORDER BY permission_name`, schema), roleID)
	if err != nil {
		return Detail{}, fmt.Errorf("get role permissions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	perms := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return Detail{}, fmt.Errorf("scan role permission: %w", err)
		}
		perms = append(perms, name)
	}
	if err := rows.Err(); err != nil {
		return Detail{}, fmt.Errorf("iterate role permissions: %w", err)
	}
	return Detail{Summary: summary, Permissions: perms}, nil
}

// UserIDsWithRole returns the users holding a live grant of roleID.
func (s *Store) UserIDsWithRole(ctx context.Context, tenantSlug, roleID string) ([]string, error) {
	query := fmt.Sprintf(`SELECT ur.user_id FROM %s.user_roles ur WHERE ur.role_id = $1 AND `+liveGrant, tenantschema.Name(tenantSlug))
	rows, err := s.db.QueryContext(ctx, query, roleID)
	if err != nil {
		return nil, fmt.Errorf("list role holders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan role holder: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role holders: %w", err)
	}
	return ids, nil
}

func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin role change: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit role change: %w", err)
	}
	return nil
}

func replacePermissions(ctx context.Context, tx *sql.Tx, schema, roleID string, permissions []string, grantedBy string) error {
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s.role_permissions WHERE role_id = $1`, schema), roleID); err != nil {
		return fmt.Errorf("clear role permissions: %w", err)
	}
	insert := fmt.Sprintf(`INSERT INTO %s.role_permissions (role_id, permission_name, granted_by) VALUES ($1, $2, NULLIF($3, '')::uuid)`, schema)
	for _, name := range slices.Compact(slices.Sorted(slices.Values(permissions))) {
		if _, err := tx.ExecContext(ctx, insert, roleID, name, grantedBy); err != nil {
			return fmt.Errorf("grant role permission %s: %w", name, err)
		}
	}
	return nil
}

// CreateRole inserts a custom role with its permissions and returns its id.
// A name already in use is ErrRoleNameTaken.
func (s *Store) CreateRole(ctx context.Context, tenantSlug, name string, description *string, permissions []string, grantedBy string, within func(tx *sql.Tx, roleID string) error) (string, error) {
	schema := tenantschema.Name(tenantSlug)
	var roleID string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, fmt.Sprintf(`INSERT INTO %s.roles (name, description) VALUES ($1, $2) RETURNING id`, schema), name, description).Scan(&roleID)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrRoleNameTaken
			}
			return fmt.Errorf("insert role: %w", err)
		}
		if err := replacePermissions(ctx, tx, schema, roleID, permissions, grantedBy); err != nil {
			return err
		}
		return within(tx, roleID)
	})
	return roleID, err
}

// lockRole locks roleID's row for the rest of tx. Locking it also blocks a
// concurrent AssignRole, whose user_roles insert needs a key-share lock on
// the same row, until tx ends.
func lockRole(ctx context.Context, tx *sql.Tx, schema, roleID string) (immutable bool, err error) {
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT is_immutable FROM %s.roles WHERE id = $1 FOR UPDATE`, schema), roleID).Scan(&immutable)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrRoleNotFound
	}
	if err != nil {
		return false, fmt.Errorf("lock role: %w", err)
	}
	return immutable, nil
}

// UpdateRole applies change to a custom role. Built-in roles are
// ErrRoleImmutable, and a name already in use is ErrRoleNameTaken.
func (s *Store) UpdateRole(ctx context.Context, tenantSlug, roleID string, change Change, grantedBy string, within InTx) error {
	schema := tenantschema.Name(tenantSlug)
	return s.inTx(ctx, func(tx *sql.Tx) error {
		immutable, err := lockRole(ctx, tx, schema, roleID)
		if err != nil {
			return err
		}
		if immutable {
			return ErrRoleImmutable
		}
		if change.Name != nil || change.Description != nil {
			_, err := tx.ExecContext(ctx, fmt.Sprintf(`
				UPDATE %s.roles SET name = COALESCE($2, name), description = CASE WHEN $3 THEN NULLIF($4, '') ELSE description END, updated_at = NOW()
				WHERE id = $1`, schema), roleID, change.Name, change.Description != nil, change.Description)
			if err != nil {
				if isUniqueViolation(err) {
					return ErrRoleNameTaken
				}
				return fmt.Errorf("update role: %w", err)
			}
		}
		if change.Permissions != nil {
			if err := replacePermissions(ctx, tx, schema, roleID, *change.Permissions, grantedBy); err != nil {
				return err
			}
		}
		return within(tx)
	})
}

// DeleteRole removes a custom role that no user holds and no pending
// invitation offers. Built-in roles are ErrRoleImmutable, and a role with a
// live grant or a pending invitation is ErrRoleInUse. Expired grants,
// accepted or revoked invitations, and a deleted account's grants and
// invitations go with the role.
func (s *Store) DeleteRole(ctx context.Context, tenantSlug, roleID string, within InTx) error {
	schema := tenantschema.Name(tenantSlug)
	return s.inTx(ctx, func(tx *sql.Tx) error {
		immutable, err := lockRole(ctx, tx, schema, roleID)
		if err != nil {
			return err
		}
		if immutable {
			return ErrRoleImmutable
		}
		var inUse bool
		inUseQuery := fmt.Sprintf(`
			SELECT EXISTS (SELECT 1 FROM %[1]s.user_roles ur WHERE ur.role_id = $1 AND `+heldByAccount+`)
			    OR EXISTS (SELECT 1 FROM %[1]s.tenant_invitations ti WHERE ti.role_id = $1 AND `+offeredToAccount+`)`, schema)
		if err := tx.QueryRowContext(ctx, inUseQuery, roleID).Scan(&inUse); err != nil {
			return fmt.Errorf("check role holders: %w", err)
		}
		if inUse {
			return ErrRoleInUse
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s.roles WHERE id = $1`, schema), roleID); err != nil {
			return fmt.Errorf("delete role: %w", err)
		}
		return within(tx)
	})
}
