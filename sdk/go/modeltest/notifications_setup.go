package modeltest

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	enginenotify "github.com/djangbahevans/goerp/internal/engine/notify"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

func newTestNotifications(t *testing.T, primaryDB *sql.DB, h *Harness, mod *module.LoadedModule, reg *registry.ModuleRegistry, rt *wasm.Runtime, moduleDir string, now func() time.Time) *TestNotifications {
	t.Helper()
	if now == nil {
		now = time.Now
	}
	n := &TestNotifications{
		t:          t,
		ctx:        t.Context(),
		db:         primaryDB,
		tenantID:   h.TenantID,
		tenantSlug: h.tenantSlug,
		moduleName: h.moduleName,
		userID:     h.UserID,
		now:        now,
	}
	if len(mod.Manifest.NotificationTypes) == 0 {
		return n
	}

	templates, err := notiftemplate.Load(mod.Manifest.NotificationTypes, moduleDir)
	if err != nil {
		t.Fatalf("modeltest: load notification templates: %v", err)
	}
	mod.NotifTemplates = templates
	n.rows, err = templates.Rows(h.moduleName)
	if err != nil {
		t.Fatalf("modeltest: notification template rows: %v", err)
	}

	tenants := tenant.NewStore(primaryDB)
	users := user.NewStore(primaryDB)
	configs := tenantconfig.NewStore(primaryDB)
	providers := providerselect.NewStore(primaryDB)
	for _, bootstrap := range []func(context.Context) error{
		tenants.Bootstrap,
		users.Bootstrap,
		configs.Bootstrap,
		billing.NewStore(primaryDB).Bootstrap,
		providers.Bootstrap,
	} {
		if err := bootstrap(t.Context()); err != nil {
			t.Fatalf("modeltest: bootstrap notification dependencies: %v", err)
		}
	}

	// Provisioning status keeps this tenant out of shared active-tenant workers.
	if _, err := tenants.ReserveSlug(t.Context(), h.TenantID, h.tenantSlug, "Module test tenant"); err != nil {
		t.Fatalf("modeltest: register notification tenant: %v", err)
	}
	t.Cleanup(func() {
		if _, err := primaryDB.ExecContext(context.Background(), `DELETE FROM system.river_job WHERE args->>'tenant_id' = $1`, h.TenantID); err != nil {
			t.Errorf("modeltest: delete notification jobs: %v", err)
		}
		if _, err := primaryDB.ExecContext(context.Background(), `DELETE FROM system.tenants WHERE id = $1`, h.TenantID); err != nil {
			t.Errorf("modeltest: delete notification tenant: %v", err)
		}
	})

	result, err := primaryDB.ExecContext(t.Context(), `INSERT INTO system.users (id, email)
        VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`, h.UserID, h.UserID+"@modeltest.invalid")
	if err != nil {
		t.Fatalf("modeltest: create notification user: %v", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("modeltest: check notification user creation: %v", err)
	}
	if inserted != 0 {
		t.Cleanup(func() {
			if _, err := primaryDB.ExecContext(context.Background(), `DELETE FROM system.users WHERE id = $1`, h.UserID); err != nil {
				t.Errorf("modeltest: delete notification user: %v", err)
			}
		})
	}
	if _, err := primaryDB.ExecContext(t.Context(), `INSERT INTO `+tenantschema.Name(h.tenantSlug)+`.tenant_members (user_id) VALUES ($1)`, h.UserID); err != nil {
		t.Fatalf("modeltest: create notification membership: %v", err)
	}

	roles := role.NewStore(primaryDB)
	if err := roles.SeedBuiltinRoles(t.Context(), h.tenantSlug); err != nil {
		t.Fatalf("modeltest: seed notification roles: %v", err)
	}
	roleID, err := roles.GetRoleByName(t.Context(), h.tenantSlug, "user")
	if err != nil {
		t.Fatalf("modeltest: find notification role: %v", err)
	}
	if err := roles.AssignRole(t.Context(), h.tenantSlug, h.UserID, roleID, ""); err != nil {
		t.Fatalf("modeltest: assign notification role: %v", err)
	}

	store := notifications.NewStore(primaryDB)
	if err := store.SeedDefaultTemplates(t.Context(), h.tenantSlug, h.moduleName, n.rows); err != nil {
		t.Fatalf("modeltest: seed bundled notification templates: %v", err)
	}
	resolver := tenantconfig.NewResolver(configs, tenants, reg)
	sender := enginenotify.NewSender(enginenotify.Deps{
		DB:        primaryDB,
		Store:     store,
		Registry:  reg,
		Config:    notifconfig.NewService(resolver, configs, nil),
		Providers: providers,
		Tenants:   tenants,
		Members:   roles,
		Jobs:      rt.EventInsertClient(),
	})
	rt.SetNotifySender(enginenotify.HostSender{Sender: sender})

	return n
}
