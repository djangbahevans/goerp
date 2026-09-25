// Package enginetables is the canonical list of engine-owned tables that
// live in every tenant schema (multitenancy-internals.md §3 "Per-tenant
// engine-owned tables"). Tenant provisioning creates every table in
// Groups, host.db's SQL validator (internal/engine/dbscope) rejects module
// SQL that references one, and the module loader rejects a model whose
// table name collides with one — so a table added here is both created
// and blocked from modules with no other change.
package enginetables

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/internal/engine/recordshares"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/savedfilters"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// Table is one engine-owned per-tenant table. A Partitioned table's
// pg_partman child partitions are named "<Name>_<suffix>" and live in the
// same tenant schema, so every name with that prefix is engine-owned too.
type Table struct {
	Name        string
	Partitioned bool
}

// Group is a set of tables created together by one Create call, e.g.
// role.Store.Bootstrap's roles/role_permissions/user_roles.
type Group struct {
	Tables []Table
	Create func(ctx context.Context, pool *sql.DB, tenantSlug string) error
}

// Groups is in creation order: tenant_invitations has a foreign key to
// roles, so the roles group comes first.
var Groups = []Group{
	{
		Tables: []Table{{Name: "roles"}, {Name: "role_permissions"}, {Name: "user_roles"}},
		Create: func(ctx context.Context, pool *sql.DB, slug string) error {
			return role.NewStore(pool).Bootstrap(ctx, slug)
		},
	},
	{
		Tables: []Table{{Name: "tenant_invitations"}},
		Create: func(ctx context.Context, pool *sql.DB, slug string) error {
			// Bootstrap never touches the resolver/mailer dependencies.
			return invite.NewStore(pool, nil, nil, nil, nil).Bootstrap(ctx, slug)
		},
	},
	{
		Tables: []Table{{Name: "record_shares"}},
		Create: func(ctx context.Context, pool *sql.DB, slug string) error {
			return recordshares.NewStore(pool).Bootstrap(ctx, slug)
		},
	},
	{
		Tables: []Table{{Name: "saved_filters"}},
		Create: func(ctx context.Context, pool *sql.DB, slug string) error {
			return savedfilters.NewStore(pool).Bootstrap(ctx, slug)
		},
	},
	{
		Tables: []Table{{Name: recordactivity.TableName}},
		Create: func(ctx context.Context, pool *sql.DB, slug string) error {
			return recordactivity.NewStore(pool).Bootstrap(ctx, slug)
		},
	},
	{
		Tables: []Table{{Name: "files"}},
		Create: func(ctx context.Context, pool *sql.DB, slug string) error {
			return files.NewStore(pool).Bootstrap(ctx, slug)
		},
	},
	{
		Tables: []Table{{Name: "module_config"}},
		Create: execEach(createModuleConfigTable),
	},
	{
		Tables: []Table{{Name: "sequences"}},
		Create: execEach(createSequencesTable),
	},
	{
		Tables: []Table{{Name: "audit_log", Partitioned: true}},
		Create: createPartitioned("audit_log", "changed_at", createAuditLogTable, createAuditLogTimeIndex),
	},
	{
		Tables: []Table{{Name: "event_log", Partitioned: true}},
		Create: createPartitioned("event_log", "emitted_at", createEventLogTable, createEventLogTimeIndex),
	},
}

// plannedTables are engine-owned per-tenant tables multitenancy-internals.md
// §3 assigns to the engine that no engine code creates yet. Their names
// are reserved now so no module can claim one first; each moves into
// Groups once the engine creates it.
var plannedTables = []string{
	"notifications",
	"notification_preferences",
	"scheduled_activities",
	"view_overrides",
}

// CreateAll creates every table in Groups in tenantSlug's schema. Every
// Create is idempotent, so a retried provisioning run is safe.
func CreateAll(ctx context.Context, pool *sql.DB, tenantSlug string) error {
	for _, g := range Groups {
		if err := g.Create(ctx, pool, tenantSlug); err != nil {
			return fmt.Errorf("create %s: %w", g.Tables[0].Name, err)
		}
	}
	return nil
}

// IsEngineOwned reports whether name is an engine-owned table in Groups,
// a planned one, or a pg_partman partition of a Partitioned one
// ("<table>_p20260901", "<table>_default", or a hand-made
// "<table>_2026_09").
func IsEngineOwned(name string) bool {
	if slices.Contains(plannedTables, name) {
		return true
	}
	for _, g := range Groups {
		for _, t := range g.Tables {
			if name == t.Name {
				return true
			}
			if suffix, ok := strings.CutPrefix(name, t.Name+"_"); ok && t.Partitioned && partitionSuffix.MatchString(suffix) {
				return true
			}
		}
	}
	return false
}

var partitionSuffix = regexp.MustCompile(`^(p?[0-9][0-9_]*|default)$`)

// execEach returns a Create that runs each statement, formatted with the
// tenant's quoted schema name, in order.
func execEach(stmts ...string) func(context.Context, *sql.DB, string) error {
	return func(ctx context.Context, pool *sql.DB, slug string) error {
		schema := tenantschema.Name(slug)
		for _, stmt := range stmts {
			if _, err := pool.ExecContext(ctx, fmt.Sprintf(stmt, schema)); err != nil {
				return err
			}
		}
		return nil
	}
}

// createPartitioned runs stmts, then registers the table with pg_partman.
// pg_partman's p_parent_table takes the plain "tenant_<slug>.<table>"
// name, not tenantschema.Name's quoted form (goerp#194).
func createPartitioned(table, controlColumn string, stmts ...string) func(context.Context, *sql.DB, string) error {
	create := execEach(stmts...)
	return func(ctx context.Context, pool *sql.DB, slug string) error {
		if err := create(ctx, pool, slug); err != nil {
			return err
		}
		return db.RegisterPartition(ctx, pool, "tenant_"+slug+"."+table, controlColumn)
	}
}

const createModuleConfigTable = `
CREATE TABLE IF NOT EXISTS %s.module_config (
    module_name TEXT NOT NULL,
    key         TEXT NOT NULL,
    value       JSONB NOT NULL,
    value_type  TEXT NOT NULL,
    encrypted   BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by  UUID,
    PRIMARY KEY (module_name, key)
)
`

// createSequencesTable backs Sequence-kind fields' per-tenant counters.
const createSequencesTable = `
CREATE TABLE IF NOT EXISTS %s.sequences (
    model       TEXT NOT NULL,
    field       TEXT NOT NULL,
    period_key  TEXT NOT NULL,
    next_value  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (model, field, period_key)
)
`

// createAuditLogTable mirrors multitenancy-internals.md's audit_log
// schema: PARTITION BY RANGE (changed_at) with a composite (id,
// changed_at) PK, since Postgres requires the partition key in every
// unique constraint on a partitioned table, plus a BRIN index on
// changed_at for append-only time-series data.
const createAuditLogTable = `
CREATE TABLE IF NOT EXISTS %s.audit_log (
    id          UUID NOT NULL DEFAULT uuidv7(),
    table_name  TEXT NOT NULL,
    record_id   UUID NOT NULL,
    operation   TEXT NOT NULL CHECK (operation IN ('INSERT','UPDATE','DELETE')),
    old_data    JSONB,
    new_data    JSONB,
    changed_by  UUID,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    request_id  TEXT,
    trace_id    TEXT,
    PRIMARY KEY (id, changed_at)
) PARTITION BY RANGE (changed_at)
`

const createAuditLogTimeIndex = `
CREATE INDEX IF NOT EXISTS idx_audit_log_time ON %s.audit_log USING BRIN (changed_at)
`

// createEventLogTable mirrors multitenancy-internals.md's event_log
// schema, partitioned the same way as audit_log. EventDeliveryWorker's
// INSERT binds emitted_at itself (rather than relying on the column
// default) so "ON CONFLICT (id, emitted_at) DO NOTHING" dedups correctly
// across a job retry.
const createEventLogTable = `
CREATE TABLE IF NOT EXISTS %s.event_log (
    id             UUID NOT NULL DEFAULT uuidv7(),
    event_name     TEXT NOT NULL,
    event_version  INT NOT NULL DEFAULT 1,
    emitter_module TEXT NOT NULL,
    payload        BYTEA NOT NULL,
    trace_id       TEXT,
    user_id        UUID,
    emitted_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, emitted_at)
) PARTITION BY RANGE (emitted_at)
`

const createEventLogTimeIndex = `
CREATE INDEX IF NOT EXISTS idx_event_log_time ON %s.event_log USING BRIN (emitted_at)
`
