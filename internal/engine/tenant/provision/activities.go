package tenantprovision

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/jobdispatch"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenant/sync"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/temporal"
)

// Activities implements every activity Workflow calls, registered as a
// whole via worker.RegisterActivity(a *Activities) — each exported method
// becomes an activity named after itself (e.g. "ReserveSlug").
type Activities struct {
	roleStore   *role.Store
	tenantStore *tenant.Store
	inviteStore *invite.Store

	schemaSyncPool *sql.DB

	syncPool   *schema.SchemaSyncPool
	diffEngine *schema.SchemaDiffEngine
	registry   *registry.ModuleRegistry

	// RiverClient is assigned after queue construction. It stays a field because Temporal
	// registers every exported activity method, making a zero-return setter invalid.
	RiverClient *river.Client[pgx.Tx]
	Runtime     *wasm.Runtime

	// platformDomain is appended to a tenant's slug to build its default
	// subdomain (RegisterDomain) — e.g. slug "acme" + platformDomain
	// "goerp.io" = "acme.goerp.io".
	platformDomain string

	// cacheClient is Redis, fail-hard constructed in Engine.New; nil only
	// in tests that never resolve the tenant by host.
	cacheClient *cache.Client

	// availableLocales (GOERP_AVAILABLE_LOCALES) are the locales seeded
	// engine-owned rows, such as the built-in activity types, carry
	// labels for.
	availableLocales []string
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
	cacheClient *cache.Client,
	availableLocales []string,
) *Activities {
	return &Activities{
		tenantStore:      tenantStore,
		inviteStore:      inviteStore,
		roleStore:        roleStore,
		schemaSyncPool:   schemaSyncPool,
		syncPool:         syncPool,
		diffEngine:       diffEngine,
		registry:         moduleRegistry,
		platformDomain:   platformDomain,
		cacheClient:      cacheClient,
		availableLocales: availableLocales,
	}
}

// Temporal application-error types ReserveSlug fails with when another
// tenant holds the slug, or the slug is reserved, and CreateTenantSchema
// fails with when the slug can't name a Postgres role.
const (
	SlugTakenErrorType    = "SlugTaken"
	SlugReservedErrorType = "SlugReserved"
	InvalidSlugErrorType  = "InvalidSlug"
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

// CreateTenantSchema creates the tenant's schema and role and their
// grants (tenantschema.Create). Idempotent. An invalid slug fails
// non-retryably, so the workflow releases the slug reservation.
func (a *Activities) CreateTenantSchema(ctx context.Context, slug string) error {
	err := tenantschema.Create(ctx, a.schemaSyncPool, slug)
	if errors.Is(err, tenantschema.ErrInvalidSlug) {
		return temporal.NewNonRetryableApplicationError(err.Error(), InvalidSlugErrorType, err)
	}
	return err
}

// CreateEngineTables creates every engine-owned per-tenant table
// (enginetables.Groups) a fresh tenant needs before module schema sync
// and config seeding can run, with their seed rows.
func (a *Activities) CreateEngineTables(ctx context.Context, slug string) error {
	return enginetables.CreateAll(ctx, a.schemaSyncPool, slug, a.availableLocales)
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

// SyncModuleSchema logs sync failures and continues provisioning rather than returning a
// module-specific error.
func (a *Activities) SyncModuleSchema(ctx context.Context, tenantID, tenantSlug, moduleName string, actor abiv1.Request) error {
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
	transition, err := tenantsync.SyncOneTransition(ctx, a.syncPool, a.diffEngine, t, mod, nil)
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantSlug).Str("module", moduleName).Msg("module schema sync failed during provisioning")
		return nil
	}

	// Fresh tenants run applicable migrations from 0.0.0 through the module version. A nil
	// RiverClient skips dispatch until the queue is available.
	jobdispatch.EnqueueApplicableDataMigrations(ctx, a.RiverClient, a.syncPool, []tenant.Tenant{t}, mod, "provisioning")
	if a.Runtime != nil {
		actor.TenantID, actor.TenantSlug = tenantID, tenantSlug
		snap.NotifyModuleChange(ctx, a.Runtime, actor, moduleName, transition.OldVersion, transition.Version)
	}

	return nil
}

func (a *Activities) SeedTenantConfig(ctx context.Context, slug string) error {
	store := tenantconfig.NewStore(a.schemaSyncPool)
	engineSeeds := make(map[string]any)
	for key, value := range notifconfig.Seeds() {
		engineSeeds[notifconfig.Namespace+"."+key] = value
	}

	if err := store.SeedModuleConfig(ctx, slug, manifest.Manifest{
		Name:              notifconfig.Namespace,
		ConfigSchema:      notifconfig.Schema,
		TenantConfigSeeds: engineSeeds,
	}); err != nil {
		return err
	}

	snap := a.registry.Snapshot()
	if snap == nil {
		return nil
	}

	for _, mod := range snap.Modules() {
		if mod.Status == module.StatusFailed {
			continue
		}

		if err := store.SeedModuleConfig(ctx, slug, mod.Manifest); err != nil {
			return err
		}
	}

	return nil
}

func (a *Activities) SeedSystemData(ctx context.Context, slug string) error {
	roleStore := role.NewStore(a.schemaSyncPool)
	if err := roleStore.SeedBuiltinRoles(ctx, slug); err != nil {
		return fmt.Errorf("seed builtin roles: %w", err)
	}

	if err := roleStore.GrantModuleDefaults(ctx, slug, permission.Builtin()); err != nil {
		return fmt.Errorf("grant builtin permissions: %w", err)
	}

	snap := a.registry.Snapshot()
	if snap == nil {
		return nil
	}

	for _, mod := range snap.Modules() {
		if mod.Status == module.StatusFailed {
			continue
		}
		if err := roleStore.GrantModuleDefaults(ctx, slug, mod.Manifest.Permissions); err != nil {
			return err
		}
	}

	return nil
}

// CreateAdminUser invites the founding admin through the standard invite flow, including
// profile creation and the invitation email.
func (a *Activities) CreateAdminUser(ctx context.Context, slug, adminEmail, adminName string) error {
	if _, err := a.inviteStore.Invite(ctx, slug, adminEmail, "admin", adminName, nil); err != nil {
		return fmt.Errorf("invite admin user: %w", err)
	}
	return nil
}

// AssignAdminRole grants the admin role to an already-registered user —
// the self-service path's step 7 (multitenancy-internals.md §6), in place
// of CreateAdminUser's invite — creating their tenant_members row first,
// since a role is granted only to a member. Idempotent, so a retried
// activity is safe.
func (a *Activities) AssignAdminRole(ctx context.Context, slug, userID string) error {
	roleID, err := a.roleStore.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		return fmt.Errorf("look up admin role: %w", err)
	}
	if err := a.roleStore.AddMember(ctx, slug, userID); err != nil {
		return err
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
// else scoped to "active tenants only") starts seeing this tenant. It then
// drops the subdomain's host-lookup cache entry, which may hold a "not
// found" from a visit before the tenant existed, so the new workspace
// resolves at once. Best-effort, like offboarding's invalidation.
func (a *Activities) ActivateTenant(ctx context.Context, slug string) error {
	if _, err := a.tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		return fmt.Errorf("activate tenant: %w", err)
	}
	if a.cacheClient != nil {
		domain := slug + "." + a.platformDomain
		if err := a.cacheClient.Delete(ctx, tenantresolve.DomainCacheKey(domain)); err != nil {
			log.Warn().Err(err).Str("slug", slug).Str("domain", domain).Msg("provision: domain cache invalidation failed")
		}
	}
	return nil
}

func (a *Activities) NotifyTenantProvisioned(ctx context.Context, tenantID, slug string, actor abiv1.Request) error {
	if a.Runtime != nil {
		actor.TenantID, actor.TenantSlug = tenantID, slug
		a.registry.Snapshot().NotifyHooks(ctx, a.Runtime, registry.HookInvocation{Hook: abiv1.HookTenantProvisioned, Request: actor})
	}

	return nil
}
