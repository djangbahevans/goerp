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
    activated_at    TIMESTAMPTZ,
    suspended_at    TIMESTAMPTZ,
    suspended_by    UUID,
    suspend_reason  TEXT,
    deleted_at      TIMESTAMPTZ,
    offboard_deletion_started_at TIMESTAMPTZ
)
`

// addOffboardDeletionStartedAtColumn backfills offboard_deletion_started_at
// onto a system.tenants table created before this column existed —
// createTenantsTable's own CREATE TABLE IF NOT EXISTS only reaches a
// brand-new database, so an already-bootstrapped one needs this separate
// idempotent ALTER TABLE the same way suspended_by's comment above
// anticipates a future one for its own REFERENCES constraint.
const addOffboardDeletionStartedAtColumn = `
ALTER TABLE system.tenants ADD COLUMN IF NOT EXISTS offboard_deletion_started_at TIMESTAMPTZ
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
		if _, err := tx.ExecContext(ctx, createTenantDomainsDomainIndex); err != nil {
			return fmt.Errorf("create tenant_domains domain index: %w", err)
		}
		if _, err := tx.ExecContext(ctx, addOffboardDeletionStartedAtColumn); err != nil {
			return fmt.Errorf("add offboard_deletion_started_at column: %w", err)
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
const tenantColumns = `id, slug, name, plan, status, region, country, trial_ends_at, created_at, updated_at, activated_at, suspended_at, suspended_by, suspend_reason, deleted_at, offboard_deletion_started_at`

// tenantColumnsPrefixed is tenantColumns qualified with the "t" alias, for
// queries joining system.tenants against another table.
const tenantColumnsPrefixed = `t.id, t.slug, t.name, t.plan, t.status, t.region, t.country, t.trial_ends_at, t.created_at, t.updated_at, t.activated_at, t.suspended_at, t.suspended_by, t.suspend_reason, t.deleted_at, t.offboard_deletion_started_at`

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so scanTenant
// works for both a single-row QueryRowContext result and one row of a
// multi-row QueryContext result.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTenant(sc rowScanner) (*Tenant, error) {
	var t Tenant
	var country, suspendedBy, suspendReason sql.NullString
	var trialEndsAt, activatedAt, suspendedAt, deletedAt, offboardDeletionStartedAt sql.NullTime

	if err := sc.Scan(
		&t.ID, &t.Slug, &t.Name, &t.Plan, &t.Status, &t.Region, &country,
		&trialEndsAt, &t.CreatedAt, &t.UpdatedAt, &activatedAt, &suspendedAt, &suspendedBy,
		&suspendReason, &deletedAt, &offboardDeletionStartedAt,
	); err != nil {
		return nil, err
	}

	if country.Valid {
		t.Country = &country.String
	}
	if trialEndsAt.Valid {
		t.TrialEndsAt = &trialEndsAt.Time
	}
	if activatedAt.Valid {
		t.ActivatedAt = &activatedAt.Time
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
	if offboardDeletionStartedAt.Valid {
		t.OffboardDeletionStartedAt = &offboardDeletionStartedAt.Time
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

// GetByID returns ErrTenantNotFound for both a nonexistent id and a
// deleted tenant — the two cases callers doing tenant resolution (e.g.
// Class B/C routes, auth-internals.md §9) must not distinguish between,
// per multitenancy-internals.md's "never reveal via a different response
// whether a tenant exists at all" rule.
func (s *Store) GetByID(ctx context.Context, id string) (*Tenant, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+tenantColumns+`
		FROM system.tenants
		WHERE id = $1 AND status != $2
	`, id, StatusDeleted)

	t, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTenantNotFound
		}
		return nil, fmt.Errorf("get tenant by id: %w", err)
	}

	return t, nil
}

// GetByDomain resolves domain (already normalised by the caller — stripped
// of port, lowercased) against system.tenant_domains, matching only a
// verified custom domain or any subdomain (createTenantDomainsDomainIndex's
// own partial index anticipates exactly this predicate). Returns
// ErrTenantNotFound for no match and for a deleted tenant alike, same
// reasoning as GetByID.
func (s *Store) GetByDomain(ctx context.Context, domain string) (*Tenant, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+tenantColumnsPrefixed+`
		FROM system.tenants t
		JOIN system.tenant_domains td ON td.tenant_id = t.id
		WHERE td.domain = $1
		  AND (td.verified_at IS NOT NULL OR td.type = $2)
		  AND t.status != $3
	`, domain, DomainSubdomain, StatusDeleted)

	t, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTenantNotFound
		}
		return nil, fmt.Errorf("get tenant by domain: %w", err)
	}

	return t, nil
}

// UpdateStatus flips a tenant's status and, only when transitioning to
// StatusSuspended, records suspended_at/suspend_reason — those two columns
// are intentionally never cleared on a later transition (e.g. unsuspend),
// preserving "when/why was this tenant last suspended" as history rather
// than losing it the moment the tenant becomes active again. Similarly,
// transitioning to StatusActive sets activated_at only the first time
// (activated_at IS NULL) — a later suspend/unsuspend cycle back to active
// leaves the original activation timestamp alone, since it's what
// `goerp tenant status`'s provisioning-duration figure (activated_at -
// created_at) is meant to measure. deleted_at follows the same
// set-once-and-keep pattern as activated_at, for the same reason:
// OffboardTenantWorkflow's MarkTenantDeleted step is the only caller that
// ever transitions to StatusDeleted, and it does so exactly once.
func (s *Store) UpdateStatus(ctx context.Context, slug string, status Status, reason *string) (*Tenant, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE system.tenants
		SET status = $1,
		    updated_at = NOW(),
		    activated_at = CASE WHEN $1 = 'active' AND activated_at IS NULL THEN NOW() ELSE activated_at END,
		    suspended_at = CASE WHEN $1 = 'suspended' THEN NOW() ELSE suspended_at END,
		    suspend_reason = CASE WHEN $1 = 'suspended' THEN $2 ELSE suspend_reason END,
		    deleted_at = CASE WHEN $1 = 'deleted' AND deleted_at IS NULL THEN NOW() ELSE deleted_at END
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

// ErrOffboardNotCancellable is returned by CancelOffboarding when the
// tenant isn't in a state offboarding can be reversed from — either it was
// never offboarding, or offboard deletion has already started
// (multitenancy-internals.md §9's "point of no return"). Callers surface
// this as cli-reference.md §5's "nothing to cancel" error.
var ErrOffboardNotCancellable = errors.New("tenant offboard is not cancellable")

// BeginOffboarding transitions an active tenant to StatusOffboarding — the
// first step of OffboardTenantWorkflow (and the immediate-offboard River
// job). Scoped to status = 'active' so it can't be called twice for the
// same tenant or fired against a tenant in some other state; returns
// ErrTenantNotFound if no active tenant with slug exists (which also
// covers "already offboarding" — a real distinction callers might want
// later, but not one this ticket's callers need to make).
func (s *Store) BeginOffboarding(ctx context.Context, slug string) (*Tenant, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE system.tenants
		SET status = $1, updated_at = NOW()
		WHERE slug = $2 AND status = $3
		RETURNING `+tenantColumns+`
	`, StatusOffboarding, slug, StatusActive)

	t, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTenantNotFound
		}
		return nil, fmt.Errorf("begin offboarding: %w", err)
	}

	return t, nil
}

// MarkDeletionStarted sets offboard_deletion_started_at, the point past
// which CancelOffboarding refuses to run. Scoped to status = 'offboarding'
// AND offboard_deletion_started_at IS NULL, so it's safe to call
// concurrently with CancelOffboarding: whichever of the two CAS updates
// commits first wins the race, and the loser sees 0 rows affected. started
// reports which one this call was — false means CancelOffboarding got
// there first and the caller (OffboardTenantWorkflow, or the immediate
// River job) must stop before running any deletion step.
func (s *Store) MarkDeletionStarted(ctx context.Context, slug string) (started bool, err error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE system.tenants
		SET offboard_deletion_started_at = NOW(), updated_at = NOW()
		WHERE slug = $1 AND status = $2 AND offboard_deletion_started_at IS NULL
	`, slug, StatusOffboarding)
	if err != nil {
		return false, fmt.Errorf("mark deletion started: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mark deletion started: %w", err)
	}

	return n > 0, nil
}

// CancelOffboarding reverses a still-cancellable offboard back to
// StatusActive. Scoped to status = 'offboarding' AND
// offboard_deletion_started_at IS NULL — the same CAS guard
// MarkDeletionStarted races against — so a tenant whose deletion has
// already started (or that was never offboarding) returns
// ErrOffboardNotCancellable rather than silently doing nothing.
func (s *Store) CancelOffboarding(ctx context.Context, slug string) (*Tenant, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE system.tenants
		SET status = $1, updated_at = NOW()
		WHERE slug = $2 AND status = $3 AND offboard_deletion_started_at IS NULL
		RETURNING `+tenantColumns+`
	`, StatusActive, slug, StatusOffboarding)

	t, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOffboardNotCancellable
		}
		return nil, fmt.Errorf("cancel offboarding: %w", err)
	}

	return t, nil
}

// DeleteProvisioning permanently removes a tenant row that never got past
// StatusProvisioning — the compensating action a failed provisioning
// workflow takes to release its slug reservation (multitenancy-
// internals.md §6 step 2's compensation, on a schema-creation failure).
// Scoped to status = 'provisioning' so it can never delete a tenant that
// reached activation, even if called with a stale or wrong id. Returns
// ErrTenantNotFound if no provisioning-status tenant with id exists.
func (s *Store) DeleteProvisioning(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM system.tenants WHERE id = $1 AND status = $2
	`, id, StatusProvisioning)
	if err != nil {
		return fmt.Errorf("delete provisioning tenant: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete provisioning tenant: %w", err)
	}
	if n == 0 {
		return ErrTenantNotFound
	}
	return nil
}

// CreateDomain registers a domain for tenantID — e.g. the default
// subdomain ProvisionTenantWorkflow's RegisterDomain activity creates
// (multitenancy-internals.md §6 step 8). A domain that already exists
// (system.tenant_domains.domain is UNIQUE) returns the underlying
// Postgres constraint-violation error unwrapped beyond %w, same
// convention CreateTenant already uses for its own uniqueness violations.
func (s *Store) CreateDomain(ctx context.Context, tenantID, domain string, domainType DomainType, isPrimary bool) (*Domain, error) {
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO system.tenant_domains (tenant_id, domain, type, is_primary)
		VALUES ($1, $2, $3, $4)
		RETURNING id, tenant_id, domain, type, is_primary, verified_at, created_at
	`, tenantID, domain, string(domainType), isPrimary)

	var d Domain
	var verifiedAt sql.NullTime
	if err := row.Scan(&d.ID, &d.TenantID, &d.Domain, &d.Type, &d.IsPrimary, &verifiedAt, &d.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert domain: %w", err)
	}
	if verifiedAt.Valid {
		d.VerifiedAt = &verifiedAt.Time
	}

	return &d, nil
}

// DomainsForTenant returns every system.tenant_domains row for tenantID,
// verified and unverified alike.
func (s *Store) DomainsForTenant(ctx context.Context, tenantID string) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, domain, type, is_primary, verified_at, created_at
		FROM system.tenant_domains
		WHERE tenant_id = $1
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query domains for tenant: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var domains []Domain
	for rows.Next() {
		var d Domain
		var verifiedAt sql.NullTime
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Domain, &d.Type, &d.IsPrimary, &verifiedAt, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan domain: %w", err)
		}
		if verifiedAt.Valid {
			d.VerifiedAt = &verifiedAt.Time
		}
		domains = append(domains, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate domains for tenant: %w", err)
	}

	return domains, nil
}
