// Package role is the per-tenant-schema RBAC tables — roles,
// role_permissions, user_roles — from auth-internals.md §10 "RBAC — roles
// and permissions". These live in each tenant's own tenant_{slug} schema,
// one physical copy per tenant, alongside tenant_invitations
// (internal/engine/invite). Scoped to table creation plus seeding the
// three built-in roles (admin/user/portal) — permission-bitfield
// representation, role inheritance resolution, and reconciling
// module-declared default grants into existing tenants are separate,
// larger scope.
package role

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

var ErrRoleNotFound = errors.New("role not found")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates roles/role_permissions/user_roles in the given
// tenant's schema if they don't already exist. Does not create the schema
// itself — assumes tenant_{slug} already exists (production: tenant
// provisioning's job; this package's own tests create a fixture schema
// directly).
//
// granted_by on role_permissions and user_id/granted_by on user_roles are
// plain UUID columns with no FK, not the literal "REFERENCES users(id)"
// auth-internals.md's role_permissions snippet shows — matching the
// pattern the same doc's user_roles.user_id already establishes
// explicitly ("no cross-schema FK, validated by the engine at assignment
// time"). A real FK from a tenant_{slug}-schema table to system.users
// would only resolve if system happened to be on the connection's
// search_path at creation time, which this package never assumes.
// Bootstrap is concurrent-safe against other calls racing to bootstrap the
// same tenant's schema (goerp#171) via db.WithAdvisoryLock, scoped to
// tenantSlug — bootstrapping two different tenants concurrently doesn't
// serialize against each other, only two callers targeting the same one
// do.
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
