package tenantsync

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
)

func TestSyncOne_UpgradeWithoutTemplatesPrunesDefaultsAndKeepsOverrides(t *testing.T) {
	for _, empty := range []struct {
		name      string
		templates *notiftemplate.ModuleTemplates
	}{
		{name: "nil"},
		{name: "empty", templates: new(notiftemplate.ModuleTemplates)},
	} {
		t.Run(empty.name, func(t *testing.T) {
			env := newTestEnv(t)
			tn := env.activeTenant(t, uniqueSlug(t))
			ctx := t.Context()
			mod := loadedModule(t, "widgets_"+tn.Slug, widgetModel())
			mod.NotifTemplates = shippedTemplates(t, "Shipped")
			store := notifications.NewStore(env.conn)

			if err := SyncOne(ctx, env.pool, env.diffEngine, tn, mod, nil); err != nil {
				t.Fatalf("install module: %v", err)
			}

			key := mod.Manifest.Name + ".widget_made"
			override := notiftemplate.Row{
				TemplateKey: key, Channel: "in_app", Locale: "en",
				Fields: map[string]string{notiftemplate.ColTitle: "Tenant content"},
			}
			if err := store.SaveTemplateOverride(ctx, tn.Slug, override); err != nil {
				t.Fatal(err)
			}

			other := override
			other.TemplateKey = "other.widget_made"
			if err := store.SeedDefaultTemplates(ctx, tn.Slug, "other", []notiftemplate.Row{other}); err != nil {
				t.Fatal(err)
			}

			mod.Manifest.Version = "2.0.0"
			mod.NotifTemplates = empty.templates
			if err := SyncOne(ctx, env.pool, env.diffEngine, tn, mod, nil); err != nil {
				t.Fatalf("upgrade module without templates: %v", err)
			}

			def, gotOverride, err := store.TemplateVersions(ctx, tn.Slug, key, "in_app", "en")
			if err != nil {
				t.Fatal(err)
			}
			if def != nil || gotOverride[notiftemplate.ColTitle] != "Tenant content" {
				t.Errorf("versions after upgrade = (%v, %v), want no default and intact override", def, gotOverride)
			}

			if _, err := store.Template(ctx, tn.Slug, other.TemplateKey, "in_app", "en"); err != nil {
				t.Errorf("other module's default: %v", err)
			}
		})
	}
}
