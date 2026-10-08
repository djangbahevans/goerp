// Package role manages tenant-schema RBAC tables, built-in roles, role assignments and
// default module grants.
package role

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

var ErrRoleNotFound = errors.New("role not found")

// ErrAdminUserNotFound is returned by AdminUserID when no user currently
// holds the tenant's built-in 'admin' role — including a tenant whose
// schema (and therefore its roles/user_roles tables) hasn't been
// provisioned yet.
var ErrAdminUserNotFound = errors.New("tenant has no admin user")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates role and member tables in an existing tenant schema. A tenant-scoped
// advisory lock serializes creation; user UUIDs are validated at assignment time without
// cross-schema foreign keys.
func (s *Store) Bootstrap(ctx context.Context, tenantSlug string) error {
	keys := []int64{db.AdvisoryLockKey("role.Bootstrap:" + tenantSlug)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		schema := tenantschema.Name(tenantSlug)

		createRoles := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.roles (
			    id           UUID PRIMARY KEY DEFAULT uuidv7(),
			    name         TEXT NOT NULL,
			    description  TEXT,
			    parent_id    UUID REFERENCES %s.roles(id),
			    is_immutable BOOLEAN NOT NULL DEFAULT FALSE,
			    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    UNIQUE (name)
			)
		`, schema, schema)
		if _, err := tx.ExecContext(ctx, createRoles); err != nil {
			return fmt.Errorf("create roles table: %w", err)
		}

		createRolePermissions := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.role_permissions (
			    role_id         UUID NOT NULL REFERENCES %s.roles(id) ON DELETE CASCADE,
			    permission_name TEXT NOT NULL,
			    granted_by      UUID,
			    granted_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    PRIMARY KEY (role_id, permission_name)
			)
		`, schema, schema)
		if _, err := tx.ExecContext(ctx, createRolePermissions); err != nil {
			return fmt.Errorf("create role_permissions table: %w", err)
		}

		createUserRoles := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.user_roles (
			    user_id     UUID NOT NULL,
			    role_id     UUID NOT NULL REFERENCES %s.roles(id) ON DELETE CASCADE,
			    granted_by  UUID,
			    granted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    expires_at  TIMESTAMPTZ,
			    PRIMARY KEY (user_id, role_id)
			)
		`, schema, schema)
		if _, err := tx.ExecContext(ctx, createUserRoles); err != nil {
			return fmt.Errorf("create user_roles table: %w", err)
		}

		if _, err := tx.ExecContext(ctx, fmt.Sprintf(createTenantMembers, schema)); err != nil {
			return fmt.Errorf("create tenant_members table: %w", err)
		}

		return nil
	})
}

// SeedBuiltinRoles idempotently inserts the three immutable built-in roles
// (auth-internals.md §10 "Built-in roles") into the given tenant's schema.
// superadmin and public are deliberately not among them — neither is a
// roles table row at all (superadmin is the platform-operator admin API
// token, public is "no authentication provided," decided by routing).
func (s *Store) SeedBuiltinRoles(ctx context.Context, tenantSlug string) error {
	schema := tenantschema.Name(tenantSlug)

	query := fmt.Sprintf(`
		INSERT INTO %s.roles (name, is_immutable)
		VALUES ('admin', true), ('user', true), ('portal', true)
		ON CONFLICT (name) DO NOTHING
	`, schema)
	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("seed built-in roles: %w", err)
	}

	return nil
}

// GetRoleByName returns just the role's id — matches
// invite.RoleResolver's signature exactly, so *Store satisfies it
// structurally with no adapter code.
func (s *Store) GetRoleByName(ctx context.Context, tenantSlug, name string) (string, error) {
	schema := tenantschema.Name(tenantSlug)

	query := fmt.Sprintf(`SELECT id FROM %s.roles WHERE name = $1`, schema)

	var id string
	if err := s.db.QueryRowContext(ctx, query, name).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrRoleNotFound
		}
		return "", fmt.Errorf("get role by name: %w", err)
	}

	return id, nil
}

// CountUsers returns the number of the tenant's members (tenant_members
// rows, suspended ones included) — the Users column cli-reference.md §5
// documents for `goerp tenant list`. Returns 0, not an error, for a tenant
// whose schema hasn't been provisioned yet.
func (s *Store) CountUsers(ctx context.Context, tenantSlug string) (int, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s.tenant_members`, schema)

	var count int
	if err := s.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		if isUndefinedTable(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("count tenant users: %w", err)
	}

	return count, nil
}

// AdminUserID returns the user_id of the tenant's admin user — the
// earliest grantee of the built-in 'admin' role in the tenant's schema.
// Returns ErrAdminUserNotFound if no user currently holds that role.
func (s *Store) AdminUserID(ctx context.Context, tenantSlug string) (string, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT ur.user_id
		FROM %s.user_roles ur
		JOIN %s.roles r ON r.id = ur.role_id
		WHERE r.name = 'admin'
		ORDER BY ur.granted_at ASC
		LIMIT 1
	`, schema, schema)

	var userID string
	if err := s.db.QueryRowContext(ctx, query).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) || isUndefinedTable(err) {
			return "", ErrAdminUserNotFound
		}
		return "", fmt.Errorf("get tenant admin user: %w", err)
	}

	return userID, nil
}

// RoleNamesForUser returns the names of every unexpired role userID holds
// in the tenant's schema (expires_at IS NULL or still in the future) — a
// lapsed grant shouldn't appear in a JWT's roles claim. A suspended member
// holds none. Returns an empty, non-nil slice for a user with no grants or
// an unprovisioned tenant.
func (s *Store) RoleNamesForUser(ctx context.Context, tenantSlug, userID string) ([]string, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT r.name
		FROM %[1]s.user_roles ur
		JOIN %[1]s.roles r ON r.id = ur.role_id
		JOIN %[1]s.tenant_members tm ON tm.user_id = ur.user_id AND tm.status = 'active'
		WHERE ur.user_id = $1 AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
		ORDER BY r.name
	`, schema)

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		if isUndefinedTable(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("get role names for user: %w", err)
	}
	defer func() { _ = rows.Close() }()

	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan role name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role names: %w", err)
	}

	return names, nil
}

// AssignRole grants or reactivates a role by clearing expiry on conflict. An empty
// grantedBy stores NULL for grants without an administering user.
func (s *Store) AssignRole(ctx context.Context, tenantSlug, userID, roleID, grantedBy string) error {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		INSERT INTO %s.user_roles (user_id, role_id, granted_by)
		VALUES ($1, $2, NULLIF($3, '')::uuid)
		ON CONFLICT (user_id, role_id) DO UPDATE SET
			granted_by = EXCLUDED.granted_by,
			granted_at = NOW(),
			expires_at = NULL
	`, schema)

	if _, err := s.db.ExecContext(ctx, query, userID, roleID, grantedBy); err != nil {
		return fmt.Errorf("assign role: %w", err)
	}
	return nil
}

// RevokeRole removes a tenant role grant. Missing grants are a no-op.
func (s *Store) RevokeRole(ctx context.Context, tenantSlug, userID, roleID string) error {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`DELETE FROM %s.user_roles WHERE user_id = $1 AND role_id = $2`, schema)

	if _, err := s.db.ExecContext(ctx, query, userID, roleID); err != nil {
		return fmt.Errorf("revoke role: %w", err)
	}
	return nil
}

// IsMember is the membership check (auth-internals.md §2 "Tenant
// members"): userID holds an unexpired role grant in the tenant and their
// tenant_members row is active. Every place that asks whether someone
// belongs to a tenant asks this.
func (s *Store) IsMember(ctx context.Context, tenantSlug, userID string) (bool, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT EXISTS (
			SELECT 1 FROM %[1]s.user_roles ur
			JOIN %[1]s.tenant_members tm ON tm.user_id = ur.user_id AND tm.status = 'active'
			WHERE ur.user_id = $1 AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
		)
	`, schema)

	var isMember bool
	if err := s.db.QueryRowContext(ctx, query, userID).Scan(&isMember); err != nil {
		if isUndefinedTable(err) {
			return false, nil
		}
		return false, fmt.Errorf("check tenant membership: %w", err)
	}

	return isMember, nil
}

// Member is an active tenant member as SearchMembers returns them. Name is
// nil when the user has no profile name.
type Member struct {
	ID           string
	Email        string
	Name         *string
	AvatarFileID *string
}

// SearchMembers returns up to limit active members of the tenant — an
// active, non-deleted user passing IsMember's check — ordered by
// name, then email, with nameless users last. A non-empty query matches
// the start of any word of the name or the start of the email,
// case-insensitively; excludeUserID, when non-empty, is left out.
func (s *Store) SearchMembers(ctx context.Context, tenantSlug, query, excludeUserID string, limit int) ([]Member, error) {
	schema := tenantschema.Name(tenantSlug)
	sqlQuery := fmt.Sprintf(`
		SELECT u.id, u.email, NULLIF(p.name, ''), p.avatar_file_id
		FROM system.users u
		LEFT JOIN system.user_profiles p ON p.user_id = u.id
		WHERE u.deleted_at IS NULL AND u.status = 'active'
		  AND EXISTS (
			SELECT 1 FROM %[1]s.user_roles ur
			JOIN %[1]s.tenant_members tm ON tm.user_id = ur.user_id AND tm.status = 'active'
			WHERE ur.user_id = u.id AND (ur.expires_at IS NULL OR ur.expires_at > NOW()))
		  AND ($1 = '' OR u.email LIKE $2 ESCAPE '\' OR p.name ~* $3)
		  AND ($4 = '' OR u.id::text <> $4)
		ORDER BY lower(NULLIF(p.name, '')) NULLS LAST, u.email
		LIMIT $5
	`, schema)
	lowered := strings.ToLower(query)
	emailPrefix := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(lowered) + "%"
	namePattern := `(^|[^[:alnum:]])` + regexp.QuoteMeta(query)

	rows, err := s.db.QueryContext(ctx, sqlQuery, query, emailPrefix, namePattern, excludeUserID, limit)
	if err != nil {
		if isUndefinedTable(err) {
			return []Member{}, nil
		}
		return nil, fmt.Errorf("search tenant members: %w", err)
	}
	defer func() { _ = rows.Close() }()

	members := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Email, &m.Name, &m.AvatarFileID); err != nil {
			return nil, fmt.Errorf("scan tenant member: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tenant members: %w", err)
	}
	return members, nil
}

// PermissionNamesForUser returns the distinct permission names granted by
// every unexpired role userID holds in the tenant's schema.
func (s *Store) PermissionNamesForUser(ctx context.Context, tenantSlug, userID string) ([]string, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT DISTINCT rp.permission_name
		FROM %[1]s.user_roles ur
		JOIN %[1]s.role_permissions rp ON rp.role_id = ur.role_id
		JOIN %[1]s.tenant_members tm ON tm.user_id = ur.user_id AND tm.status = 'active'
		WHERE ur.user_id = $1 AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
		ORDER BY rp.permission_name
	`, schema)

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		if isUndefinedTable(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("get permission names for user: %w", err)
	}
	defer func() { _ = rows.Close() }()

	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan permission name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate permission names: %w", err)
	}

	return names, nil
}

// RoleIDsForUser returns the role_id of every unexpired role userID holds
// in the tenant's schema — same predicate RoleNamesForUser uses, without
// the join to roles.name, so a suspended member gets an empty set.
// Populates permcache's Redis role-assignment cache (auth-internals.md §14 layer 2).
func (s *Store) RoleIDsForUser(ctx context.Context, tenantSlug, userID string) ([]string, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT ur.role_id
		FROM %[1]s.user_roles ur
		JOIN %[1]s.tenant_members tm ON tm.user_id = ur.user_id AND tm.status = 'active'
		WHERE ur.user_id = $1 AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
		ORDER BY ur.role_id
	`, schema)

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		if isUndefinedTable(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("get role ids for user: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan role id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role ids: %w", err)
	}

	return ids, nil
}

// Role is one roles table row — enough to resolve inheritance
// (auth-internals.md §10 "Role inheritance") without a second round trip.
type Role struct {
	ID       string
	Name     string
	ParentID *string
}

// AllRoles returns every role in the tenant's schema, parent_id included.
// permcache.RolePermissionMap.RebuildAll walks these to resolve each
// role's full, inheritance-merged permission set.
func (s *Store) AllRoles(ctx context.Context, tenantSlug string) ([]Role, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`SELECT id, name, parent_id FROM %s.roles`, schema)

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		if isUndefinedTable(err) {
			return []Role{}, nil
		}
		return nil, fmt.Errorf("get all roles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	roles := []Role{}
	for rows.Next() {
		var r Role
		var parentID sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &parentID); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		if parentID.Valid {
			r.ParentID = &parentID.String
		}
		roles = append(roles, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate roles: %w", err)
	}

	return roles, nil
}

// AllRolePermissions returns every role_permissions grant in the tenant's
// schema, keyed by role_id — one query for the whole tenant rather than
// one per role, since permcache.RolePermissionMap.RebuildAll needs every
// role's own grants to resolve inheritance.
func (s *Store) AllRolePermissions(ctx context.Context, tenantSlug string) (map[string][]string, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`SELECT role_id, permission_name FROM %s.role_permissions`, schema)

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		if isUndefinedTable(err) {
			return map[string][]string{}, nil
		}
		return nil, fmt.Errorf("get all role permissions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byRole := map[string][]string{}
	for rows.Next() {
		var roleID, permName string
		if err := rows.Scan(&roleID, &permName); err != nil {
			return nil, fmt.Errorf("scan role permission: %w", err)
		}
		byRole[roleID] = append(byRole[roleID], permName)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role permissions: %w", err)
	}

	return byRole, nil
}

// isUndefinedTable reports whether err is Postgres' undefined_table error
// (42P01) — the error a query against a tenant_{slug} schema's tables gets
// when that tenant's schema (or role.Store.Bootstrap for it) hasn't run
// yet, as opposed to a genuine query failure.
func isUndefinedTable(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "42P01"
}

func isUniqueViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505"
}
