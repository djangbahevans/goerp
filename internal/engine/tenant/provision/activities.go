package tenantprovision

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"sort"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/jobdispatch"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/recordshares"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenant/sync"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

// Activities implements every activity Workflow calls, registered as a
// whole via worker.RegisterActivity(a *Activities) — each exported method
// becomes an activity named after itself (e.g. "ReserveSlug").
type Activities struct {
	// tenantStore and inviteStore are the engine's own, already-constructed
	// instances (matching engine.go's existing Stage 1 wiring) — normal
	// DML operations, no elevated privilege needed.
	tenantStore *tenant.Store
	inviteStore *invite.Store

	// schemaSyncPool is the same pool internal/engine/schema's Stage 4
	// sync mechanism already uses for DDL (config.Config's
	// DBSchemaSyncDSN) — used here for every DDL statement (CREATE
	// SCHEMA, CREATE TABLE) and the throwaway Bootstrap-only role/invite
	// stores below. data-layer.md documents a schema_sync_user/app_user
	// privilege split for these two pools in production, but nothing in
	// this codebase provisions those two Postgres roles yet (grepped —
	// zero references), so this package doesn't issue GRANT statements
	// either; that's separate, ops-level future work once the roles
	// actually exist.
	schemaSyncPool *sql.DB

	syncPool   *schema.SchemaSyncPool
	diffEngine *schema.SchemaDiffEngine
	registry   *registry.ModuleRegistry

	// RiverClient inserts the data migration jobs SyncModuleSchema
	// triggers after a successful sync. Exported and set directly rather
	// than threaded through NewActivities or a setter method: engine.go
	// constructs Activities before the job queue client exists (same
	// "doesn't exist until here" ordering NewActivities' own call site
	// already documents for moduleRegistry/diffEngine) but every
	// Activities method only runs later, at workflow-execution time, well
	// after this field has had a chance to be set — and it must be a
	// plain field, not a method: systemWorker.RegisterActivity(a) reflects
	// over every exported method of *Activities and requires each one to
	// look like an activity function (error, or (result, error)), so an
	// exported zero-return setter method would itself break activity
	// registration.
	RiverClient *river.Client[pgx.Tx]

	// platformDomain is appended to a tenant's slug to build its default
	// subdomain (RegisterDomain) — e.g. slug "acme" + platformDomain
	// "goerp.io" = "acme.goerp.io".
	platformDomain string
}

func NewActivities(
	tenantStore *tenant.Store,
	inviteStore *invite.Store,
	schemaSyncPool *sql.DB,
	syncPool *schema.SchemaSyncPool,
	diffEngine *schema.SchemaDiffEngine,
	moduleRegistry *registry.ModuleRegistry,
	platformDomain string,
) *Activities {
	return &Activities{
		tenantStore:    tenantStore,
		inviteStore:    inviteStore,
		schemaSyncPool: schemaSyncPool,
		syncPool:       syncPool,
		diffEngine:     diffEngine,
		registry:       moduleRegistry,
		platformDomain: platformDomain,
	}
}

// ReserveSlug inserts the tenant row (system.tenants.status defaults to
// 'provisioning' — CreateTenant's own default), reserving the slug via
// its UNIQUE constraint. Returns the new tenant's id.
func (a *Activities) ReserveSlug(ctx context.Context, slug, name string) (string, error) {
	t, err := a.tenantStore.CreateTenant(ctx, slug, name)
	if err != nil {
		return "", fmt.Errorf("reserve slug: %w", err)
	}
	return t.ID, nil
}

// ReleaseSlugReservation is the compensating action a failed provisioning
// run takes after CreateTenantSchema fails (multitenancy-internals.md §6
// step 2's compensation) — removes the reserved tenant row so the slug
// becomes available again.
func (a *Activities) ReleaseSlugReservation(ctx context.Context, tenantID string) error {
	if err := a.tenantStore.DeleteProvisioning(ctx, tenantID); err != nil {
		return fmt.Errorf("release slug reservation: %w", err)
	}
	return nil
}

// CreateTenantSchema creates tenant_{slug} if it doesn't already exist —
// idempotent, so a workflow retry after a transient failure here doesn't
// itself fail on "schema already exists".
func (a *Activities) CreateTenantSchema(ctx context.Context, slug string) error {
	if _, err := a.schemaSyncPool.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+tenantschema.Name(slug)); err != nil {
		return fmt.Errorf("create tenant schema: %w", err)
	}
	return nil
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
// schema (goerp#194) — PARTITION BY RANGE (changed_at) with a composite
// (id, changed_at) PK, since Postgres requires the partition key in
// every unique constraint on a partitioned table, plus a BRIN index on
// changed_at (efficient for append-only time-series data). No
// REVOKE SELECT ... FROM app_user, since no schema_sync_user/app_user
// Postgres role split exists anywhere in this codebase yet (see this
// file's own schemaSyncPool doc comment above). partman.create_parent is
// called against this table immediately after creation, in
// CreateEngineTables below.
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
// schema (goerp#194) — PARTITION BY RANGE (emitted_at) with a composite
// (id, emitted_at) PK and a BRIN index on emitted_at, both required by
// the same partition-key constraint createAuditLogTable's doc comment
// above explains. EventDeliveryWorker's INSERT binds emitted_at itself
// (rather than relying on the column default) so
// "ON CONFLICT (id, emitted_at) DO NOTHING" dedups correctly across a
// job retry — see that package's own doc comment.
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

// registerTenantPartition wraps db.RegisterPartition for a per-tenant
// table, building the plain "tenant_<slug>.<table>" name pg_partman's
// p_parent_table parameter needs (see that function's own doc comment for
// why the quoted tenantschema.Name form can't be used here — goerp#194).
func registerTenantPartition(ctx context.Context, pool *sql.DB, slug, table, controlColumn string) error {
	return db.RegisterPartition(ctx, pool, "tenant_"+slug+"."+table, controlColumn)
}

// CreateEngineTables creates the engine-owned tables a fresh tenant needs
// before module schema sync and config seeding can run: roles/
// role_permissions/user_roles (role.Store.Bootstrap), tenant_invitations
// (invite.Store.Bootstrap — requires roles to exist first, per its own
// foreign key), record_shares (recordshares.Store.Bootstrap — goerp#472,
// the .Shareable() model widening's compiled RLS EXISTS lookup reads
// this uniformly regardless of which module owns the model), module_config
// (this ticket's own SeedTenantConfig step), sequences (backing store for
// Sequence-kind fields' per-tenant counters, keyed by
// (model, field, period_key)), audit_log (goerp#363 — host.orm's write
// path records one row here per INSERT/UPDATE/DELETE on a module's own
// audited_tables[]), and event_log (goerp#16 — eventdelivery.Worker
// records one row here per dispatched domain event).
// multitenancy-internals.md §6 step 3 lists several further engine-owned
// tables (files, notifications, notification_preferences, saved_filters,
// view_overrides) — deliberately not created here: nothing in this
// codebase reads or writes any of them yet (each is its own separate,
// untriaged feature area). Creating unconsumed tables now would be
// schema no code can yet verify against.
func (a *Activities) CreateEngineTables(ctx context.Context, slug string) error {
	if err := role.NewStore(a.schemaSyncPool).Bootstrap(ctx, slug); err != nil {
		return fmt.Errorf("bootstrap roles: %w", err)
	}

	// invite.NewStore's UserResolver/RoleResolver/Mailer args are never
	// touched by Bootstrap itself (it only creates tenant_invitations) —
	// nil is safe here, this instance exists for exactly one call.
	if err := invite.NewStore(a.schemaSyncPool, nil, nil, nil, nil).Bootstrap(ctx, slug); err != nil {
		return fmt.Errorf("bootstrap invitations: %w", err)
	}

	if err := recordshares.NewStore(a.schemaSyncPool).Bootstrap(ctx, slug); err != nil {
		return fmt.Errorf("bootstrap record_shares: %w", err)
	}

	query := fmt.Sprintf(createModuleConfigTable, tenantschema.Name(slug))
	if _, err := a.schemaSyncPool.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("create module_config table: %w", err)
	}

	seqQuery := fmt.Sprintf(createSequencesTable, tenantschema.Name(slug))
	if _, err := a.schemaSyncPool.ExecContext(ctx, seqQuery); err != nil {
		return fmt.Errorf("create sequences table: %w", err)
	}

	schemaName := tenantschema.Name(slug)

	auditQuery := fmt.Sprintf(createAuditLogTable, schemaName)
	if _, err := a.schemaSyncPool.ExecContext(ctx, auditQuery); err != nil {
		return fmt.Errorf("create audit_log table: %w", err)
	}
	auditIndexQuery := fmt.Sprintf(createAuditLogTimeIndex, schemaName)
	if _, err := a.schemaSyncPool.ExecContext(ctx, auditIndexQuery); err != nil {
		return fmt.Errorf("create audit_log time index: %w", err)
	}
	if err := registerTenantPartition(ctx, a.schemaSyncPool, slug, "audit_log", "changed_at"); err != nil {
		return err
	}

	eventLogQuery := fmt.Sprintf(createEventLogTable, schemaName)
	if _, err := a.schemaSyncPool.ExecContext(ctx, eventLogQuery); err != nil {
		return fmt.Errorf("create event_log table: %w", err)
	}
	eventLogIndexQuery := fmt.Sprintf(createEventLogTimeIndex, schemaName)
	if _, err := a.schemaSyncPool.ExecContext(ctx, eventLogIndexQuery); err != nil {
		return fmt.Errorf("create event_log time index: %w", err)
	}
	if err := registerTenantPartition(ctx, a.schemaSyncPool, slug, "event_log", "emitted_at"); err != nil {
		return err
	}

	return nil
}

// ListModuleNames returns the name of every currently loaded module that
// isn't StatusFailed, sorted for predictable ordering — Workflow calls
// SyncModuleSchema once per name returned here.
func (a *Activities) ListModuleNames(ctx context.Context) ([]string, error) {
	snap := a.registry.Snapshot()
	if snap == nil {
		return nil, nil
	}

	names := make([]string, 0, len(snap.Modules()))
	for name, mod := range snap.Modules() {
		if mod.Status == module.StatusFailed {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	return names, nil
}

// SyncModuleSchema runs schema sync for one (tenant, module) pair via
// tenantsync.SyncOne. Never returns an error — a module schema-sync
// failure is logged and does not block the rest of provisioning (this
// ticket's own acceptance criteria), matching tenantsync.SyncAll's own
// per-tenant failure isolation exactly.
func (a *Activities) SyncModuleSchema(ctx context.Context, tenantID, tenantSlug, moduleName string) error {
	snap := a.registry.Snapshot()
	if snap == nil {
		return nil
	}
	mod, ok := snap.Modules()[moduleName]
	if !ok {
		log.Warn().Str("tenant", tenantSlug).Str("module", moduleName).Msg("module schema sync: module no longer loaded, skipping")
		return nil
	}

	t := tenant.Tenant{ID: tenantID, Slug: tenantSlug}
	if err := tenantsync.SyncOne(ctx, a.syncPool, a.diffEngine, t, mod, nil); err != nil {
		log.Error().Err(err).Str("tenant", tenantSlug).Str("module", moduleName).Msg("module schema sync failed during provisioning")
		return nil
	}

	// Trigger data migration dispatch (engine-internals.md §2 Stage 4 step
	// 26, migration-guide.md §4 "Running during provisioning") — a fresh
	// tenant runs every applicable handler from 0.0.0 up to the module's
	// current version, the same watermark-driven evaluation an upgrade
	// uses, no special case needed. a.RiverClient nil (never wired — a
	// test constructing Activities directly, or a real engine still
	// starting up before Start()) is a no-op inside the helper below.
	jobdispatch.EnqueueApplicableDataMigrations(ctx, a.RiverClient, a.syncPool, []tenant.Tenant{t}, mod, "provisioning")

	return nil
}

const upsertModuleConfig = `
INSERT INTO %s.module_config (module_name, key, value, value_type)
VALUES ($1, $2, $3, $4)
ON CONFLICT (module_name, key) DO NOTHING
`

// SeedTenantConfig inserts every loaded module's declared
// TenantConfigSeeds into module_config. ON CONFLICT DO NOTHING is load-
// bearing, not just tidy: a Temporal workflow retry after a transient
// failure later in the run replays this activity, and seeds must not be
// re-applied over whatever an operator may have already changed since.
func (a *Activities) SeedTenantConfig(ctx context.Context, slug string) error {
	snap := a.registry.Snapshot()
	if snap == nil {
		return nil
	}

	schemaName := tenantschema.Name(slug)
	query := fmt.Sprintf(upsertModuleConfig, schemaName)

	for _, mod := range snap.Modules() {
		if mod.Status == module.StatusFailed {
			continue
		}
		for key, value := range mod.Manifest.TenantConfigSeeds {
			valueJSON, err := json.Marshal(value, json.Deterministic(true))
			if err != nil {
				return fmt.Errorf("encode config seed %s.%s: %w", mod.Manifest.Name, key, err)
			}
			if _, err := a.schemaSyncPool.ExecContext(ctx, query, mod.Manifest.Name, key, valueJSON, jsonValueType(value)); err != nil {
				return fmt.Errorf("seed config %s.%s: %w", mod.Manifest.Name, key, err)
			}
		}
	}

	return nil
}

func jsonValueType(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case float64, int, int64:
		return "number"
	case nil:
		return "null"
	case []any:
		return "array"
	default:
		return "object"
	}
}

// SeedSystemData seeds the three built-in roles, then grants every loaded
// module's declared default-role permissions (Manifest.Permissions[].
// DefaultRoles). A module naming a default role that doesn't resolve
// (e.g. a typo, or a role only some other module would have declared) is
// skipped rather than failing the whole run — provisioning shouldn't
// abort over one module's manifest referencing a role that doesn't
// exist.
func (a *Activities) SeedSystemData(ctx context.Context, slug string) error {
	roleStore := role.NewStore(a.schemaSyncPool)
	if err := roleStore.SeedBuiltinRoles(ctx, slug); err != nil {
		return fmt.Errorf("seed builtin roles: %w", err)
	}

	snap := a.registry.Snapshot()
	if snap == nil {
		return nil
	}

	schemaName := tenantschema.Name(slug)
	grantQuery := fmt.Sprintf(`
		INSERT INTO %s.role_permissions (role_id, permission_name)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, schemaName)

	for _, mod := range snap.Modules() {
		if mod.Status == module.StatusFailed {
			continue
		}
		for _, perm := range mod.Manifest.Permissions {
			for _, roleName := range perm.DefaultRoles {
				roleID, err := roleStore.GetRoleByName(ctx, slug, roleName)
				if err != nil {
					if errors.Is(err, role.ErrRoleNotFound) {
						continue
					}
					return fmt.Errorf("resolve default role %q for permission %q: %w", roleName, perm.Name, err)
				}
				if _, err := a.schemaSyncPool.ExecContext(ctx, grantQuery, roleID, perm.Name); err != nil {
					return fmt.Errorf("grant %q to role %q: %w", perm.Name, roleName, err)
				}
			}
		}
	}

	return nil
}

// CreateAdminUser invites the tenant's founding admin via the same invite
// mechanism a normal in-app "invite a teammate" call uses (invite.Store.
// Invite) — creates the user (status 'invited'), a tenant_invitations
// row, and sends the invite email; no separate "welcome email" activity
// needed. AdminName has no home yet: invite.Store.Invite (goerp#148) has
// no display-name parameter, so it isn't captured here either — an
// existing gap in that package, not something this ticket adds scope to
// fix.
func (a *Activities) CreateAdminUser(ctx context.Context, slug, adminEmail string) error {
	if _, err := a.inviteStore.Invite(ctx, slug, adminEmail, "admin", nil); err != nil {
		return fmt.Errorf("invite admin user: %w", err)
	}
	return nil
}

// RegisterDomain creates the tenant's default subdomain
// ("{slug}.{platformDomain}"), marked primary.
func (a *Activities) RegisterDomain(ctx context.Context, tenantID, slug string) error {
	domain := slug + "." + a.platformDomain
	if _, err := a.tenantStore.CreateDomain(ctx, tenantID, domain, tenant.DomainSubdomain, true); err != nil {
		return fmt.Errorf("register domain: %w", err)
	}
	return nil
}

// ActivateTenant flips the tenant to StatusActive — the last step, after
// which ActiveTenants (and therefore Stage 4 schema sync, and anything
// else scoped to "active tenants only") starts seeing this tenant.
func (a *Activities) ActivateTenant(ctx context.Context, slug string) error {
	if _, err := a.tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		return fmt.Errorf("activate tenant: %w", err)
	}
	return nil
}
