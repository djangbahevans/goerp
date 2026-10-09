package tenantprovision

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/adminapi"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	tenantsync "github.com/djangbahevans/goerp/internal/engine/tenant/sync"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const (
	devSlug     = "dev"
	devName     = "GoERP module development"
	devEmail    = "admin@dev.localhost"
	devPassword = "GoERP-Dev-2026!"
)

type DevBootstrap struct {
	activities *Activities
	users      *user.Store
	billing    *billing.Store
	hasher     *password.Hasher
	roles      *permcache.RolePermissionMap
}

func NewDevBootstrap(activities *Activities, users *user.Store, billingStore *billing.Store, hasher *password.Hasher, roles *permcache.RolePermissionMap) *DevBootstrap {
	return &DevBootstrap{
		activities: activities,
		users:      users,
		billing:    billingStore,
		hasher:     hasher,
		roles:      roles,
	}
}

func (d *DevBootstrap) BootstrapDev(ctx context.Context, moduleName string) (adminapi.DevSession, error) {
	var result adminapi.DevSession

	// Independent engine processes share this tenant; a database lock also serializes
	// recovery of a partially completed bootstrap across those processes.
	err := db.WithAdvisoryLock(ctx, d.activities.schemaSyncPool, []int64{db.AdvisoryLockKey("module.dev.bootstrap")}, func(*sql.Tx) error {
		var err error
		result, err = d.bootstrap(ctx, moduleName)
		return err
	})

	return result, err
}

func (d *DevBootstrap) bootstrap(ctx context.Context, moduleName string) (adminapi.DevSession, error) {
	a := d.activities
	snap := a.registry.Snapshot()
	if snap == nil {
		return adminapi.DevSession{}, fmt.Errorf("module registry is unavailable")
	}
	mod, ok := snap.Modules()[moduleName]
	if !ok || mod.Status == module.StatusFailed {
		return adminapi.DevSession{}, fmt.Errorf("module %q is not loaded successfully", moduleName)
	}

	t, err := a.tenantStore.GetBySlug(ctx, devSlug)
	if errors.Is(err, tenant.ErrTenantNotFound) {
		id, err := a.ReserveSlug(ctx, devSlug, devName, uuid.NewV7().String())
		if err != nil {
			return adminapi.DevSession{}, err
		}
		t = &tenant.Tenant{ID: id, Slug: devSlug, Name: devName, Status: tenant.StatusProvisioning}
	} else if err != nil {
		return adminapi.DevSession{}, err
	}
	if t.Name != devName || (t.Status != tenant.StatusActive && t.Status != tenant.StatusProvisioning) {
		return adminapi.DevSession{}, fmt.Errorf("tenant dev is not an active or provisioning module-dev tenant")
	}

	userID, err := d.adminUser(ctx)
	if err != nil {
		return adminapi.DevSession{}, err
	}

	for _, step := range []func(context.Context, string) error{
		a.CreateTenantSchema,
		a.CreateEngineTables,
		a.SeedTenantConfig,
		a.SeedSystemData,
	} {
		if err := step(ctx, devSlug); err != nil {
			return adminapi.DevSession{}, err
		}
	}

	for _, loaded := range module.OrderByDependencies(slices.Collect(maps.Values(snap.Modules()))) {
		if loaded.Status == module.StatusFailed {
			continue
		}
		if err := tenantsync.SyncOneUnversioned(ctx, a.syncPool, a.diffEngine, *t, loaded); err != nil {
			return adminapi.DevSession{}, fmt.Errorf("sync module %s for dev: %w", loaded.Manifest.Name, err)
		}
		if err := d.billing.UpsertEntitlementOverride(ctx, t.ID, "module."+loaded.Manifest.Name, "true", new("local module development"), nil, nil); err != nil {
			return adminapi.DevSession{}, err
		}
		if err := d.billing.SetModuleEnabledForTenant(ctx, t.ID, loaded.Manifest.Name, true, nil); err != nil {
			return adminapi.DevSession{}, err
		}
	}
	if err := a.AssignAdminRole(ctx, devSlug, userID); err != nil {
		return adminapi.DevSession{}, err
	}

	domains, err := a.tenantStore.DomainsForTenant(ctx, t.ID)
	if err != nil {
		return adminapi.DevSession{}, err
	}
	registered := false
	for _, domain := range domains {
		if domain.Domain == "dev.localhost" {
			registered = true
			break
		}
	}
	if !registered {
		if err := a.RegisterDomain(ctx, t.ID, devSlug); err != nil {
			return adminapi.DevSession{}, err
		}
	}
	if err := a.ActivateTenant(ctx, devSlug); err != nil {
		return adminapi.DevSession{}, err
	}
	if err := d.roles.RebuildTenant(ctx, a.roleStore, func() *permission.PermissionRegistry { return snap.PermissionRegistry() }, devSlug); err != nil {
		return adminapi.DevSession{}, err
	}
	if a.cacheClient != nil {
		if err := tenantresolve.InvalidateTenantDomains(ctx, a.tenantStore, a.cacheClient, t.ID); err != nil {
			return adminapi.DevSession{}, fmt.Errorf("invalidate dev domains: %w", err)
		}
		if err := permcache.NewRoleCache(a.cacheClient).Invalidate(ctx, t.ID, userID); err != nil {
			return adminapi.DevSession{}, err
		}
		if err := a.cacheClient.Delete(ctx, tenantresolve.EntitlementCacheKey(t.ID)); err != nil {
			return adminapi.DevSession{}, fmt.Errorf("invalidate dev entitlements: %w", err)
		}
	}

	return adminapi.DevSession{TenantID: t.ID, Email: devEmail, Password: devPassword}, nil
}

func (d *DevBootstrap) adminUser(ctx context.Context) (string, error) {
	slot, err := d.hasher.Acquire(ctx)
	if err != nil {
		return "", err
	}
	defer slot.Release()

	u, err := d.users.GetByEmail(ctx, devEmail)
	if errors.Is(err, user.ErrUserNotFound) {
		hash, err := slot.Hash(devPassword)
		if err != nil {
			return "", fmt.Errorf("hash dev password: %w", err)
		}
		id, err := d.users.CreateRegistered(ctx, devEmail, hash, user.StatusActive)
		if err != nil {
			return "", err
		}
		if err := d.users.EnsureProfile(ctx, id, "Dev Admin"); err != nil {
			return "", err
		}
		return id, nil
	}
	if err != nil {
		return "", err
	}
	if u.Status != user.StatusActive || u.PasswordHash == nil {
		return "", fmt.Errorf("%s is not an active module-dev account", devEmail)
	}
	match, _, err := slot.Verify(devPassword, *u.PasswordHash)
	if err != nil || !match {
		return "", fmt.Errorf("%s does not use the module-dev password; existing credentials are preserved", devEmail)
	}
	if err := d.users.EnsureProfile(ctx, u.ID, "Dev Admin"); err != nil {
		return "", err
	}

	return u.ID, nil
}
