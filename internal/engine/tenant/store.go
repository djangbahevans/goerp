package tenant

import (
	"context"
	"database/sql"
	"fmt"
)

const createSchema = `CREATE SCHEMA IF NOT EXISTS system`

// createTenantsTable matches multitenancy-internals.md §2's tenants
// definition, except suspended_by omits its documented
// "REFERENCES users(id)" — no system.users table exists in this repo yet
// (a separate, unfiled ticket). The column stays a plain UUID so that
// constraint can be added via ALTER TABLE once users lands, rather than
// blocking this table on a table that doesn't exist.
const createTenantsTable = `
CREATE TABLE IF NOT EXISTS system.tenants (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    slug            TEXT NOT NULL UNIQUE
                        CHECK (slug ~ '^[a-z][a-z0-9\-]{1,62}[a-z0-9]$'),
    name            TEXT NOT NULL,
    plan            TEXT NOT NULL DEFAULT 'starter'
                        CHECK (plan IN ('starter','pro','enterprise','internal')),
    status          TEXT NOT NULL DEFAULT 'provisioning'
                        CHECK (status IN ('provisioning','active','suspended','offboarding','deleted')),
    region          TEXT NOT NULL DEFAULT 'default',
    trial_ends_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    suspended_at    TIMESTAMPTZ,
    suspended_by    UUID,
    suspend_reason  TEXT,
    deleted_at      TIMESTAMPTZ
)
`

const createTenantDomainsTable = `
CREATE TABLE IF NOT EXISTS system.tenant_domains (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id   UUID NOT NULL REFERENCES system.tenants(id) ON DELETE CASCADE,
    domain      TEXT NOT NULL UNIQUE,
    type        TEXT NOT NULL DEFAULT 'subdomain'
                    CHECK (type IN ('subdomain', 'custom')),
    is_primary  BOOLEAN NOT NULL DEFAULT FALSE,
    verified_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`

// createTenantDomainsDomainIndex is narrower than the full unique index
// `domain`'s own UNIQUE constraint already creates — it only helps a query
// that repeats this same predicate in its WHERE clause (e.g. a routing
// lookup deliberately excluding unverified custom domains from matching:
// "WHERE domain = $1 AND (verified_at IS NOT NULL OR type = 'subdomain')"),
// not a bare "WHERE domain = $1" equality lookup, which the UNIQUE index
// already serves on its own. Required by multitenancy-internals.md §2.
const createTenantDomainsDomainIndex = `
CREATE INDEX IF NOT EXISTS idx_tenant_domains_domain ON system.tenant_domains(domain)
    WHERE verified_at IS NOT NULL OR type = 'subdomain'
`

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates system.tenants and system.tenant_domains (and their
// partial index) if they don't already exist. Idempotent — safe to call
// on every engine startup, same as schema.SchemaSyncPool.Bootstrap.
func (s *Store) Bootstrap(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, createSchema); err != nil {
		return fmt.Errorf("create system schema: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, createTenantsTable); err != nil {
		return fmt.Errorf("create tenants table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, createTenantDomainsTable); err != nil {
		return fmt.Errorf("create tenant_domains table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, createTenantDomainsDomainIndex); err != nil {
		return fmt.Errorf("create tenant_domains domain index: %w", err)
	}

	return nil
}

// CreateTenant inserts a new tenant with the given slug/name, taking every
// other column's default (plan "starter", status "provisioning", region
// "default"). A slug that already exists, or fails the slug format check,
// returns the underlying Postgres constraint-violation error unwrapped
// beyond the standard %w — callers needing to distinguish "duplicate slug"
// from "invalid slug format" inspect the returned *pgconn.PgError's
// ConstraintName themselves; this package doesn't hide that behind a
// sentinel error, since which constraint fired is itself informative.
func (s *Store) CreateTenant(ctx context.Context, slug, name string) (*Tenant, error) {
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO system.tenants (slug, name)
		VALUES ($1, $2)
		RETURNING id, slug, name, plan, status, region, created_at, updated_at
	`, slug, name)

	var t Tenant
	if err := row.Scan(&t.ID, &t.Slug, &t.Name, &t.Plan, &t.Status, &t.Region, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, fmt.Errorf("insert tenant: %w", err)
	}

	return &t, nil
}

// ActiveTenants returns every tenant with status = 'active' — "active
// tenants" per multitenancy-internals.md §16's schema-sync definition,
// the set Stage 4 schema sync runs against. Provisioning/suspended/
// offboarding/deleted tenants are excluded: a provisioning tenant may not
// have its tenant_{slug} schema created yet, and sync has no business
// touching a suspended/offboarding/deleted tenant's schema at all.
func (s *Store) ActiveTenants(ctx context.Context) ([]Tenant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, slug, name, plan, status, region, created_at, updated_at
		FROM system.tenants
		WHERE status = $1
		ORDER BY slug
	`, StatusActive)
	if err != nil {
		return nil, fmt.Errorf("query active tenants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tenants []Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Plan, &t.Status, &t.Region, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan tenant: %w", err)
		}
		tenants = append(tenants, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active tenants: %w", err)
	}

	return tenants, nil
}
