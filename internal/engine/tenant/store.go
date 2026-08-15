package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

var ErrTenantNotFound = errors.New("tenant not found")

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
    country         TEXT,
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

// addCountryColumn backfills the country column onto a system.tenants
// table created before it was added to createTenantsTable — CREATE TABLE
// IF NOT EXISTS is a no-op against an already-existing table, so a fresh
// column added to that constant only reaches tables bootstrapped after
// the change without this. Additive and nullable; never touches existing
// rows' data.
const addCountryColumn = `ALTER TABLE system.tenants ADD COLUMN IF NOT EXISTS country TEXT`

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates system.tenants and system.tenant_domains (and their
// partial index) if they don't already exist. Idempotent — safe to call
// on every engine startup, same as schema.SchemaSyncPool.Bootstrap.
// Concurrent-safe against other processes calling Bootstrap at the same
// time (goerp#171) via db.WithAdvisoryLock.
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("tenant.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createTenantsTable); err != nil {
			return fmt.Errorf("create tenants table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createTenantDomainsTable); err != nil {
			return fmt.Errorf("create tenant_domains table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, addCountryColumn); err != nil {
			return fmt.Errorf("add country column: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createTenantDomainsDomainIndex); err != nil {
			return fmt.Errorf("create tenant_domains domain index: %w", err)
		}

		return nil
	})
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

// tenantColumns is the full column set scanTenant expects, in order —
// shared by every method below that returns a complete Tenant (as opposed
// to CreateTenant/ActiveTenants' narrower, pre-existing column lists,
// which are left as they were to avoid disturbing their own tests).
const tenantColumns = `id, slug, name, plan, status, region, country, trial_ends_at, created_at, updated_at, suspended_at, suspended_by, suspend_reason, deleted_at`

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so scanTenant
// works for both a single-row QueryRowContext result and one row of a
// multi-row QueryContext result.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTenant(sc rowScanner) (*Tenant, error) {
	var t Tenant
	var country, suspendedBy, suspendReason sql.NullString
	var trialEndsAt, suspendedAt, deletedAt sql.NullTime

	if err := sc.Scan(
		&t.ID, &t.Slug, &t.Name, &t.Plan, &t.Status, &t.Region, &country,
		&trialEndsAt, &t.CreatedAt, &t.UpdatedAt, &suspendedAt, &suspendedBy,
		&suspendReason, &deletedAt,
	); err != nil {
		return nil, err
	}

	if country.Valid {
		t.Country = &country.String
	}
	if trialEndsAt.Valid {
		t.TrialEndsAt = &trialEndsAt.Time
	}
	if suspendedAt.Valid {
		t.SuspendedAt = &suspendedAt.Time
	}
	if suspendedBy.Valid {
		t.SuspendedBy = &suspendedBy.String
	}
	if suspendReason.Valid {
		t.SuspendReason = &suspendReason.String
	}
	if deletedAt.Valid {
		t.DeletedAt = &deletedAt.Time
	}

	return &t, nil
}

type ListFilter struct {
	Status Status
	Plan   Plan
	Limit  int
	// Cursor is the last-seen tenant ID from a previous page's nextCursor,
	// or "" for the first page. Tenant IDs are uuidv7() — lexicographic
	// (and therefore ID-comparison) order already matches creation order,
	// so ordering by id gives a single-column keyset cursor with no
	// separate tiebreaker column needed (contrast data-layer.md's
	// (created_at, id) pattern, which only needs the extra column because
	// created_at alone isn't unique).
	Cursor string
}

// List returns up to f.Limit tenants ordered by id (creation order),
// optionally filtered by status/plan, plus nextCursor — the value to pass
// as the next call's Cursor, or "" if this was the last page.
func (s *Store) List(ctx context.Context, f ListFilter) (tenants []Tenant, nextCursor string, err error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+tenantColumns+`
		FROM system.tenants
		WHERE ($1 = '' OR status = $1)
		  AND ($2 = '' OR plan = $2)
		  AND ($3 = '' OR id > $3::uuid)
		ORDER BY id
		LIMIT $4
	`, string(f.Status), string(f.Plan), f.Cursor, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("list tenants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan tenant: %w", err)
		}
		tenants = append(tenants, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate tenants: %w", err)
	}

	if len(tenants) > limit {
		nextCursor = tenants[limit-1].ID
		tenants = tenants[:limit]
	}

	return tenants, nextCursor, nil
}

// GetBySlug returns ErrTenantNotFound (not a wrapped sql.ErrNoRows) when no
// tenant has the given slug, so callers can errors.Is-check it directly.
func (s *Store) GetBySlug(ctx context.Context, slug string) (*Tenant, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+tenantColumns+`
		FROM system.tenants
		WHERE slug = $1
	`, slug)

	t, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTenantNotFound
		}
		return nil, fmt.Errorf("get tenant by slug: %w", err)
	}

	return t, nil
}

// UpdateStatus flips a tenant's status and, only when transitioning to
// StatusSuspended, records suspended_at/suspend_reason — those two columns
// are intentionally never cleared on a later transition (e.g. unsuspend),
// preserving "when/why was this tenant last suspended" as history rather
// than losing it the moment the tenant becomes active again.
func (s *Store) UpdateStatus(ctx context.Context, slug string, status Status, reason *string) (*Tenant, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE system.tenants
		SET status = $1,
		    updated_at = NOW(),
		    suspended_at = CASE WHEN $1 = 'suspended' THEN NOW() ELSE suspended_at END,
		    suspend_reason = CASE WHEN $1 = 'suspended' THEN $2 ELSE suspend_reason END
		WHERE slug = $3
		RETURNING `+tenantColumns+`
	`, string(status), reason, slug)

	t, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTenantNotFound
		}
		return nil, fmt.Errorf("update tenant status: %w", err)
	}

	return t, nil
}
