// Package tenantschema is the one place the tenant_{slug} schema-naming
// convention lives — every per-tenant-schema table (roles/role_permissions/
// user_roles, tenant_invitations, and eventually event_log/audit_log/
// notifications/saved_filters per auth-internals.md §10) schema-qualifies
// its queries with Name(slug) rather than relying on a session's
// search_path, since a pooled PgBouncer connection can't safely carry a
// per-request SET search_path the way a dedicated, held-for-the-session
// connection (schema.SchemaSyncPool's BeginSync) can. A tenant's Postgres
// role has the same name as its schema (data-layer.md §2.2).
package tenantschema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Name quotes tenant_{slug} as a safe identifier, ready to interpolate
// directly into SQL. Safe because slug's character set is already
// DB-constrained (system.tenants' own CHECK:
// ^[a-z][a-z0-9\-]{1,62}[a-z0-9]$) by the time it reaches here. It names
// both the tenant's schema and its role.
func Name(slug string) string {
	return `"tenant_` + slug + `"`
}

// ErrInvalidSlug reports a slug system.create_tenant_role rejects, such as
// one too long for tenant_{slug} to fit a Postgres identifier.
var ErrInvalidSlug = errors.New("invalid tenant slug")

// Create creates the tenant's role and schema, gives db.EngineRole DML on
// every table and sequence later created in the schema, and gives the
// tenant role USAGE on it. Idempotent; pool is the schema-sync pool.
func Create(ctx context.Context, pool *sql.DB, slug string) error {
	if _, err := pool.ExecContext(ctx, "SELECT system.create_tenant_role($1)", slug); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "22023" {
			return fmt.Errorf("create tenant role: %w: %s", ErrInvalidSlug, pgErr.Message)
		}
		return fmt.Errorf("create tenant role: %w", err)
	}
	name := Name(slug)
	stmts := []string{
		"CREATE SCHEMA IF NOT EXISTS " + name,
		"GRANT USAGE ON SCHEMA " + name + " TO " + db.EngineRole + ", " + name,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA " + name + " GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + db.EngineRole,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA " + name + " GRANT USAGE, SELECT ON SEQUENCES TO " + db.EngineRole,
	}
	for _, stmt := range stmts {
		if _, err := pool.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create tenant schema: %w", err)
		}
	}
	return nil
}

// Drop removes the tenant's pg_partman registrations and templates, schema
// and role, attempting every step even if one fails. Idempotent; pool is
// the schema-sync pool.
func Drop(ctx context.Context, pool *sql.DB, slug string) error {
	var errs []error
	if err := dropPartitionRegistrations(ctx, pool, slug); err != nil {
		errs = append(errs, fmt.Errorf("remove tenant partition registrations: %w", err))
	}
	if _, err := pool.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+Name(slug)+" CASCADE"); err != nil {
		errs = append(errs, fmt.Errorf("drop tenant schema: %w", err))
	}
	if _, err := pool.ExecContext(ctx, "SELECT system.drop_tenant_role($1)", slug); err != nil {
		errs = append(errs, fmt.Errorf("drop tenant role: %w", err))
	}
	return errors.Join(errs...)
}

// dropPartitionRegistrations deletes the tenant's part_config rows and the
// template tables pg_partman made for them, which live in its own schema.
func dropPartitionRegistrations(ctx context.Context, pool *sql.DB, slug string) error {
	rows, err := pool.QueryContext(ctx,
		"DELETE FROM partman.part_config WHERE parent_table LIKE $1 RETURNING template_table",
		"tenant\\_"+slug+".%")
	if err != nil {
		return err
	}
	var templates []string
	for rows.Next() {
		var tmpl sql.NullString
		if err := rows.Scan(&tmpl); err != nil {
			_ = rows.Close()
			return err
		}
		if tmpl.Valid {
			templates = append(templates, tmpl.String)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, tmpl := range templates {
		schemaName, table, ok := strings.Cut(tmpl, ".")
		if !ok {
			continue
		}
		if _, err := pool.ExecContext(ctx, "DROP TABLE IF EXISTS "+pgx.Identifier{schemaName, table}.Sanitize()); err != nil {
			return err
		}
	}
	return nil
}
