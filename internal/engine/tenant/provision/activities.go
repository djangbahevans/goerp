package tenantprovision

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/jobdispatch"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenant/sync"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/temporal"
)

// Activities implements every activity Workflow calls, registered as a
// whole via worker.RegisterActivity(a *Activities) — each exported method
// becomes an activity named after itself (e.g. "ReserveSlug").
type Activities struct {
	// tenantStore and inviteStore are the engine's own, already-constructed
	roleStore *role.Store
	// instances (matching engine.go's existing Stage 1 wiring) — normal
	// DML operations, no elevated privilege needed.
	tenantStore *tenant.Store
	inviteStore *invite.Store

	// schemaSyncPool is the same pool internal/engine/schema's Stage 4
	// sync mechanism uses for DDL (config.Config's DBSchemaSyncDSN), whose
	// role owns every tenant table (data-layer.md §2.2) — used here for
	// every DDL statement and grant, and the throwaway Bootstrap-only
	// role/invite stores below.
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
	roleStore *role.Store,
	schemaSyncPool *sql.DB,
	syncPool *schema.SchemaSyncPool,
	diffEngine *schema.SchemaDiffEngine,
	moduleRegistry *registry.ModuleRegistry,
	platformDomain string,
) *Activities {
	return &Activities{
		tenantStore:    tenantStore,
		inviteStore:    inviteStore,
		roleStore:      roleStore,
		schemaSyncPool: schemaSyncPool,
		syncPool:       syncPool,
		diffEngine:     diffEngine,
		registry:       moduleRegistry,
		platformDomain: platformDomain,
	}
}

// Temporal application-error types ReserveSlug fails with when another
// tenant holds the slug, or the slug is reserved.
const (
	SlugTakenErrorType    = "SlugTaken"
	SlugReservedErrorType = "SlugReserved"
)

// ReserveSlug inserts the tenant row under the workflow-chosen tenantID
// (system.tenants.status defaults to 'provisioning'), reserving the slug
// via its UNIQUE constraint. Idempotent for tenantID, so a retry after an
// unreported success returns the same id. A slug another tenant holds
// fails non-retryably: retrying can't free it.
func (a *Activities) ReserveSlug(ctx context.Context, slug, name, tenantID string) (string, error) {
	id, err := a.tenantStore.ReserveSlug(ctx, tenantID, slug, name)
	if errors.Is(err, tenant.ErrSlugTaken) {
		return "", temporal.NewNonRetryableApplicationError("tenant slug is already taken", SlugTakenErrorType, err)
	}
	if errors.Is(err, tenant.ErrSlugReserved) {
		return "", temporal.NewNonRetryableApplicationError("tenant slug is reserved", SlugReservedErrorType, err)
	}
	if err != nil {
		return "", fmt.Errorf("reserve slug: %w", err)
	}
	return id, nil
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

// CreateTenantSchema creates tenant_{slug} and gives db.EngineRole DML on
// every table and sequence later created in it. Idempotent.
func (a *Activities) CreateTenantSchema(ctx context.Context, slug string) error {
	schemaName := tenantschema.Name(slug)
	stmts := []string{
		"CREATE SCHEMA IF NOT EXISTS " + schemaName,
		"GRANT USAGE ON SCHEMA " + schemaName + " TO " + db.EngineRole,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA " + schemaName + " GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + db.EngineRole,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA " + schemaName + " GRANT USAGE, SELECT ON SEQUENCES TO " + db.EngineRole,
	}
	for _, stmt := range stmts {
		if _, err := a.schemaSyncPool.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create tenant schema: %w", err)
		}
	}
	return nil
}

// CreateEngineTables creates every engine-owned per-tenant table
// (enginetables.Groups) a fresh tenant needs before module schema sync
// and config seeding can run.
func (a *Activities) CreateEngineTables(ctx context.Context, slug string) error {
	return enginetables.CreateAll(ctx, a.schemaSyncPool, slug)
}

// ListModuleNames returns the name of every currently loaded module that
// isn't StatusFailed, in dependency order — Workflow calls
// SyncModuleSchema once per name returned here, so a module's schema sync
// runs after the schema of every module it depends on.
func (a *Activities) ListModuleNames(ctx context.Context) ([]string, error) {
	snap := a.registry.Snapshot()
	if snap == nil {
		return nil, nil
	}

	mods := make([]*module.LoadedModule, 0, len(snap.Modules()))
	for _, mod := range snap.Modules() {
		if mod.Status == module.StatusFailed {
			continue
		}
		mods = append(mods, mod)
	}

	ordered := module.OrderByDependencies(mods)
	names := make([]string, len(ordered))
	for i, mod := range ordered {
		names[i] = mod.Manifest.Name
	}

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
// Invite) — creates the user (status 'invited'), a system.user_profiles
// row (goerp#817), a tenant_invitations row, and sends the invite email;
// no separate "welcome email" activity needed.
func (a *Activities) CreateAdminUser(ctx context.Context, slug, adminEmail, adminName string) error {
	if _, err := a.inviteStore.Invite(ctx, slug, adminEmail, "admin", adminName, nil); err != nil {
		return fmt.Errorf("invite admin user: %w", err)
	}
	return nil
}

// AssignAdminRole grants the admin role to an already-registered user —
// the self-service path's step 7 (multitenancy-internals.md §6), in place
// of CreateAdminUser's invite. Idempotent, so a retried activity is safe.
func (a *Activities) AssignAdminRole(ctx context.Context, slug, userID string) error {
	roleID, err := a.roleStore.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		return fmt.Errorf("look up admin role: %w", err)
	}
	if err := a.roleStore.AssignRole(ctx, slug, userID, roleID, ""); err != nil {
		return fmt.Errorf("assign admin role: %w", err)
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
