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
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
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

	configs := tenantconfig.NewStore(primaryDB)
	providers := providerselect.NewStore(primaryDB)
	for _, bootstrap := range []func(context.Context) error{
		billing.NewStore(primaryDB).Bootstrap,
		providers.Bootstrap,
	} {
		if err := bootstrap(t.Context()); err != nil {
			t.Fatalf("modeltest: bootstrap notification dependencies: %v", err)
		}
	}

	store := notifications.NewStore(primaryDB)
	if err := store.SeedDefaultTemplates(t.Context(), h.tenantSlug, h.moduleName, n.rows); err != nil {
		t.Fatalf("modeltest: seed bundled notification templates: %v", err)
	}
	resolver := tenantconfig.NewResolver(configs, h.tenants, reg)
	sender := enginenotify.NewSender(enginenotify.Deps{
		DB:        primaryDB,
		Store:     store,
		Registry:  reg,
		Config:    notifconfig.NewService(resolver, configs, nil),
		Providers: providers,
		Tenants:   h.tenants,
		Members:   h.roles,
		Jobs:      rt.EventInsertClient(),
	})
	rt.SetNotifySender(enginenotify.HostSender{Sender: sender})

	return n
}
