package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// ModuleSyncStatus is one module's sync record for one tenant — a row of
// system.module_schema_versions.
type ModuleSyncStatus struct {
	ModuleName     string     `json:"module_name"`
	CurrentVersion string     `json:"current_version"`
	Status         string     `json:"status"` // "ok" | "failed" | "in_progress"
	SyncedAt       *time.Time `json:"synced_at,omitempty"`
}

const createModuleSchemaVersionsTable = `
CREATE TABLE IF NOT EXISTS system.module_schema_versions (
    tenant_id               UUID        NOT NULL,
    module_name             TEXT        NOT NULL,
    current_version         TEXT        NOT NULL,
    schema_synced_at        TIMESTAMPTZ,
    schema_sync_status      TEXT        NOT NULL DEFAULT 'in_progress',
    data_migration_version  TEXT,
    data_migration_status   TEXT,
    PRIMARY KEY (tenant_id, module_name)
)
`

// createPendingConstraintValidationsTable backs constraints created NOT
// VALID by apply.go's Execute (goerp#20) — one row per constraint awaiting
// a background VALIDATE CONSTRAINT run. tenant_slug is denormalized
// alongside tenant_id so schema.ValidateConstraintWorker never needs a
// separate tenant lookup to build its SET search_path.
const createPendingConstraintValidationsTable = `
CREATE TABLE IF NOT EXISTS system.pending_constraint_validations (
    tenant_id        UUID        NOT NULL,
    tenant_slug      TEXT        NOT NULL,
    table_name       TEXT        NOT NULL,
    constraint_name  TEXT        NOT NULL,
    status           TEXT        NOT NULL DEFAULT 'pending',
    error            TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    validated_at     TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, table_name, constraint_name)
)
`

// createSchemaSyncAcceptancesTable backs goerp#292's `POST
// /admin/schema/accept` — an audit trail an accepted row is never deleted
// from, only ever gaining a consumed_at once ExecuteAccepted actually
// applies it (see apply.go's markAcceptancesConsumed). consumed_at exists
// specifically so a hash isn't matchable forever: target_hash is
// changeHash's output — a change's structural shape (kind/table/column),
// not an identifier tied to the one diff run that produced it — so
// without consumed_at, an unrelated future diff that happens to propose a
// structurally identical change (e.g. a column dropped, later re-added
// under the same name, then dropped again) would silently match a stale
// acceptance an operator reviewed for a completely different event.
// module_version pins an acceptance to the exact manifest version Accept
// diffed against — the same structural hash can otherwise recur across
// an unrelated version bump (or even within one version, for a
// ModifyColumn whose Detail string doesn't capture every reason it was
// blocked), so AcceptedHashes only ever matches a hash recorded under
// the version currently being synced. The partial unique index enforces
// "at most one live (unconsumed) acceptance per (tenant, module, hash,
// version)" — both so markAcceptancesConsumed's UPDATE (matched on the
// same key) can only ever affect the one row that actually authorized
// an apply, and so two concurrent POST /admin/schema/accept calls for
// the same still-blocked change can't each insert their own row.
const createSchemaSyncAcceptancesTable = `
CREATE TABLE IF NOT EXISTS system.schema_sync_acceptances (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id      UUID NOT NULL,
    module_name    TEXT NOT NULL,
    module_version TEXT NOT NULL,
    target_hash    TEXT NOT NULL,
    reason         TEXT NOT NULL,
    operator       TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    consumed_at    TIMESTAMPTZ
)
`

const createSchemaSyncAcceptancesUnconsumedIndex = `
CREATE UNIQUE INDEX IF NOT EXISTS schema_sync_acceptances_unconsumed_idx
    ON system.schema_sync_acceptances (tenant_id, module_name, module_version, target_hash)
    WHERE consumed_at IS NULL
`

type SchemaSyncPool struct {
	primary            *sql.DB
	lockAcquireTimeout time.Duration
}

func NewPool(pool *sql.DB, lockAcquireTimeout time.Duration) *SchemaSyncPool {
	return &SchemaSyncPool{primary: pool, lockAcquireTimeout: lockAcquireTimeout}
}

// Raw returns the underlying connection pool — connected as
// schema_sync_user, which has BYPASSRLS (multitenancy-internals.md §5a:
// "DDL and bulk data migrations need unfiltered access to every row
// regardless of any tenant's ABAC policies"). goerp tenant export
// (goerp#156) is the one other caller with that same requirement: a bulk
// administrative dump, not a request served on behalf of a specific end
// user, so it must not go through the RLS-constrained primary pool
// host.orm reads use.
func (p *SchemaSyncPool) Raw() *sql.DB {
	return p.primary
}

// Bootstrap creates system.module_schema_versions if it doesn't already
// exist. Concurrent-safe against other processes calling Bootstrap at the
// same time (goerp#171) via db.WithAdvisoryLock.
func (p *SchemaSyncPool) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("schema.Bootstrap")}
	return db.WithAdvisoryLock(ctx, p.primary, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createModuleSchemaVersionsTable); err != nil {
			return fmt.Errorf("create module_schema_versions table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createPendingConstraintValidationsTable); err != nil {
			return fmt.Errorf("create pending_constraint_validations table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createSchemaSyncAcceptancesTable); err != nil {
			return fmt.Errorf("create schema_sync_acceptances table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createSchemaSyncAcceptancesUnconsumedIndex); err != nil {
			return fmt.Errorf("create schema_sync_acceptances unconsumed index: %w", err)
		}

		return nil
	})
}

// StatusForTenant returns every module_schema_versions row for the given
// tenant — the raw material for the "N of M modules synced" ratio
// GET /admin/tenants/{slug} reports (adminapi.SyncStatusReader). The
// denominator is deliberately "modules with a sync record for this
// tenant," not "every module currently loaded by the engine" — a module
// that has never attempted sync for this tenant has nothing meaningful to
// report either way, so this needs no dependency on a live module
// registry.
func (p *SchemaSyncPool) StatusForTenant(ctx context.Context, tenantID string) ([]ModuleSyncStatus, error) {
	rows, err := p.primary.QueryContext(ctx, `
		SELECT module_name, current_version, schema_sync_status, schema_synced_at
		FROM system.module_schema_versions
		WHERE tenant_id = $1
		ORDER BY module_name
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query module sync status: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var statuses []ModuleSyncStatus
	for rows.Next() {
		var s ModuleSyncStatus
		var syncedAt sql.NullTime
		if err := rows.Scan(&s.ModuleName, &s.CurrentVersion, &s.Status, &syncedAt); err != nil {
			return nil, fmt.Errorf("scan module sync status: %w", err)
		}
		if syncedAt.Valid {
			s.SyncedAt = &syncedAt.Time
		}
		statuses = append(statuses, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate module sync status: %w", err)
	}

	return statuses, nil
}

// TenantModuleStatus is one module_schema_versions row joined against the
// owning tenant's slug — the cross-tenant shape `GET /admin/schema/status`
// (goerp#292) reports, unlike StatusForTenant's single-tenant one.
type TenantModuleStatus struct {
	TenantSlug     string     `json:"tenant"`
	ModuleName     string     `json:"module_name"`
	CurrentVersion string     `json:"current_version"`
	Status         string     `json:"status"` // "ok" | "failed" | "in_progress"
	SyncedAt       *time.Time `json:"synced_at,omitempty"`
}

// StatusFiltered returns every module_schema_versions row joined to its
// tenant's slug, optionally narrowed by tenant slug / module name / a
// literal schema_sync_status value ("ok"/"failed"/"in_progress" — the
// only values ever written; "pending" per cli-reference.md §4 isn't a
// stored status at all, and is filtered by the caller after this call
// returns, since answering it needs a live Diff this package's own
// pool.go has no business running — see internal/engine/tenant/sync's
// Admin.Status).
func (p *SchemaSyncPool) StatusFiltered(ctx context.Context, tenantSlug, moduleName, status string) ([]TenantModuleStatus, error) {
	rows, err := p.primary.QueryContext(ctx, `
		SELECT t.slug, v.module_name, v.current_version, v.schema_sync_status, v.schema_synced_at
		FROM system.module_schema_versions v
		JOIN system.tenants t ON t.id = v.tenant_id
		WHERE ($1 = '' OR t.slug = $1)
		  AND ($2 = '' OR v.module_name = $2)
		  AND ($3 = '' OR v.schema_sync_status = $3)
		ORDER BY t.slug, v.module_name
	`, tenantSlug, moduleName, status)
	if err != nil {
		return nil, fmt.Errorf("query schema sync status: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var statuses []TenantModuleStatus
	for rows.Next() {
		var s TenantModuleStatus
		var syncedAt sql.NullTime
		if err := rows.Scan(&s.TenantSlug, &s.ModuleName, &s.CurrentVersion, &s.Status, &syncedAt); err != nil {
			return nil, fmt.Errorf("scan schema sync status: %w", err)
		}
		if syncedAt.Valid {
			s.SyncedAt = &syncedAt.Time
		}
		statuses = append(statuses, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema sync status: %w", err)
	}

	return statuses, nil
}

// TableCount returns the number of tables in the tenant's own Postgres
// schema (tenant_{slug}) — the schema table count cli-reference.md §5
// documents as part of `goerp tenant status`'s output. A tenant whose
// schema hasn't been created yet reports 0, the same as a real empty
// schema would — information_schema.tables simply has no matching rows.
func (p *SchemaSyncPool) TableCount(ctx context.Context, tenantSlug string) (int, error) {
	var count int
	err := p.primary.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = $1
	`, "tenant_"+tenantSlug).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count tenant schema tables: %w", err)
	}

	return count, nil
}

func (p *SchemaSyncPool) BeginSync(ctx context.Context, tenantID, tenantSlug, moduleName string, manifest *manifest.Manifest) (*SchemaSyncSession, error) {
	conn, err := p.primary.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire schema sync connection: %w", err)
	}

	lockCtx, cancel := context.WithTimeout(ctx, p.lockAcquireTimeout)
	defer cancel()

	lockA, lockB := advisoryLockKeys(tenantSlug, moduleName)
	if _, err := conn.ExecContext(lockCtx, "SELECT pg_advisory_lock($1, $2)", lockA, lockB); err != nil {
		_ = conn.Close()
		if errors.Is(lockCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("timed out waiting for schema sync lock for %s/%s (another process is syncing this pair): %w", tenantSlug, moduleName, lockCtx.Err())
		}

		return nil, fmt.Errorf("acquire schema sync lock for %s/%s: %w", tenantSlug, moduleName, err)
	}

	if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET search_path = tenant_%s", tenantSlug)); err != nil {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1, $2)", lockA, lockB)
		_ = conn.Close()
		return nil, err
	}

	return &SchemaSyncSession{
		conn:       conn,
		tenantID:   tenantID,
		tenantSlug: tenantSlug,
		moduleName: moduleName,
		manifest:   manifest,
	}, nil
}

// BeginRead opens a session for Diff only — never taking BeginSync's
// pg_advisory_lock. A diff issues no DDL, so it doesn't need to serialize
// against a concurrent sync (or another diff) the way applying changes
// does; taking the same lock a real sync holds for its DDL's whole
// duration would make goerp#292's documented "synchronous, side-effect-
// free read" (GET /admin/modules/{name}/schema, GET /admin/schema/status)
// block on, and potentially time out against, unrelated write traffic for
// no correctness benefit. The whole read still runs inside one
// REPEATABLE READ read-only transaction (see SchemaSyncSession.readTx's
// own doc comment) so it can't observe a concurrent sync half-applied —
// a read-only transaction never blocks, or is blocked by, a concurrent
// writer's DML/DDL in Postgres, so this costs nothing a bare connection
// wouldn't already pay.
func (p *SchemaSyncPool) BeginRead(ctx context.Context, tenantID, tenantSlug, moduleName string, manifest *manifest.Manifest) (*SchemaSyncSession, error) {
	conn, err := p.primary.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire schema read connection: %w", err)
	}

	if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET search_path = tenant_%s", tenantSlug)); err != nil {
		_ = conn.Close()
		return nil, err
	}

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("begin read-only schema snapshot: %w", err)
	}

	return &SchemaSyncSession{
		conn:       conn,
		tenantID:   tenantID,
		tenantSlug: tenantSlug,
		moduleName: moduleName,
		manifest:   manifest,
		readTx:     tx,
	}, nil
}

func advisoryLockKeys(tenantSlug, moduleName string) (int32, int32) {
	h := fnv.New32a()
	h.Write([]byte(tenantSlug))
	a := int32(h.Sum32())
	h.Reset()
	h.Write([]byte(moduleName))
	b := int32(h.Sum32())
	return a, b
}
