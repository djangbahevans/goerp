package engine

import (
	"context"
	"maps"
	"strings"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/enginenotif"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestNew_ReconcilesEngineNotificationDefaultsForActiveTenants(t *testing.T) {
	cfg := baseTestConfig(t)
	ctx := t.Context()
	conn, err := db.New(cfg.DBSchemaSyncDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	store := notifications.NewStore(conn)
	tenants := tenant.NewStore(conn)

	shipped, err := enginenotif.DefaultRows()
	if err != nil || len(shipped) < 2 {
		t.Fatalf("engine defaults = %v, %v; need at least two variants", shipped, err)
	}
	changed := shipped[0]
	changed.Fields = map[string]string{notiftemplate.ColTitle: "Old binary content"}
	removed := notiftemplate.Row{
		TemplateKey: "engine.removed", Channel: "in_app", Locale: "en",
		Fields: map[string]string{notiftemplate.ColTitle: "Removed default"},
	}
	override := changed
	override.Fields = map[string]string{notiftemplate.ColTitle: "Tenant wording"}
	suffix := strings.ReplaceAll(uuid.New().String(), "-", "")[:12]
	activeSlugs := []string{"activeone" + suffix, "activetwo" + suffix}
	inactiveSlugs := []string{"provisioning" + suffix, "suspended" + suffix}

	for _, tc := range []struct {
		slug   string
		status tenant.Status
	}{
		{slug: activeSlugs[0], status: tenant.StatusActive},
		{slug: activeSlugs[1], status: tenant.StatusActive},
		{slug: inactiveSlugs[0], status: tenant.StatusProvisioning},
		{slug: inactiveSlugs[1], status: tenant.StatusSuspended},
	} {
		tn, err := tenants.CreateTenant(ctx, tc.slug, tc.slug)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := tenantschema.Drop(context.Background(), conn, tc.slug); err != nil {
				t.Errorf("drop tenant schema and role: %v", err)
			}
		})
		if err := tenantschema.Create(ctx, conn, tc.slug); err != nil {
			t.Fatal(err)
		}
		if err := enginetables.CreateAll(ctx, conn, tc.slug, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, "UPDATE system.tenants SET status = $1 WHERE id = $2", tc.status, tn.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.SeedDefaultTemplates(ctx, tc.slug, enginenotif.Module, []notiftemplate.Row{changed, removed}); err != nil {
			t.Fatal(err)
		}
		for _, row := range []notiftemplate.Row{override, removed} {
			if err := store.SaveTemplateOverride(ctx, tc.slug, row); err != nil {
				t.Fatal(err)
			}
		}

		t.Cleanup(func() {
			_, _ = conn.Exec("DELETE FROM system.tenants WHERE id = $1", tn.ID)
		})
	}

	for range 2 {
		e, err := New(cfg)
		requireEngineConstruction(t, err)
		closeTestEnginePools(t, e)
		t.Cleanup(func() {
			_ = e.wasmRuntime.Close(context.Background())
			_ = e.cacheClient.Close()
			if e.temporalClient != nil {
				e.temporalClient.Close()
			}
		})

		for _, slug := range activeSlugs {
			for _, row := range shipped {
				def, gotOverride, err := store.TemplateVersions(ctx, slug, row.TemplateKey, row.Channel, row.Locale)
				if err != nil {
					t.Fatal(err)
				}
				if !maps.Equal(def, row.Fields) {
					t.Errorf("%s default %s/%s/%s = %v, want %v", slug, row.TemplateKey, row.Channel, row.Locale, def, row.Fields)
				}
				if row.TemplateKey == changed.TemplateKey && row.Channel == changed.Channel && row.Locale == changed.Locale && !maps.Equal(gotOverride, override.Fields) {
					t.Errorf("%s override = %v, want %v", slug, gotOverride, override.Fields)
				}
			}

			def, gotOverride, err := store.TemplateVersions(ctx, slug, removed.TemplateKey, removed.Channel, removed.Locale)
			if err != nil {
				t.Fatal(err)
			}
			if def != nil || !maps.Equal(gotOverride, removed.Fields) {
				t.Errorf("%s removed variant = (%v, %v), want no default and intact override", slug, def, gotOverride)
			}
		}

		for _, slug := range inactiveSlugs {
			def, _, err := store.TemplateVersions(ctx, slug, changed.TemplateKey, changed.Channel, changed.Locale)
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(def, changed.Fields) {
				t.Errorf("inactive tenant %s default = %v, want unchanged %v", slug, def, changed.Fields)
			}
		}
	}
}
