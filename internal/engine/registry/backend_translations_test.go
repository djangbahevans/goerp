package registry

import (
	"context"
	"database/sql"
	"maps"
	"sync"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/l10n/translationstore"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
)

func translationRegistry(t *testing.T) (*ModuleRegistry, *sql.DB, string) {
	t.Helper()
	conn, err := db.New("postgres://goerp:dev@localhost:15432/goerp")
	if err != nil {
		t.Skipf("Postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	store := translationstore.New(conn)
	if err := store.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}

	prefix := "translations_" + uuid.New().String()
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_translations WHERE module_name IN ($1, $2)`, prefix, prefix+"_other")
	})
	return &ModuleRegistry{Translations: store}, conn, prefix
}

func translatedModule(name, version string, entries map[string]map[string]string) *module.LoadedModule {
	return &module.LoadedModule{
		Manifest:            manifest.Manifest{Name: name, Version: version},
		Status:              module.StatusReady,
		BackendTranslations: entries,
	}
}

func TestBackendTranslationsPublicationMergeAndUninstall(t *testing.T) {
	reg, conn, name := translationRegistry(t)
	other := name + "_other"
	initial := map[string]*module.LoadedModule{
		name: translatedModule(name, "1.0.0", map[string]map[string]string{
			"en": {"changed": "original", "kept": "retained", "empty": ""},
			"fr": {"kept": "conservé"},
		}),
		other: translatedModule(other, "1.0.0", map[string]map[string]string{"en": {"changed": "other"}}),
	}

	if _, err := reg.UpdateWithContext(t.Context(), func(map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) { return initial, nil }); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM system.module_translations WHERE module_name = $1 AND translation_key LIKE $2`, name, name+".%").Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != 4 {
		t.Fatalf("stored keys = %d, want 4", count)
	}

	upgraded := translatedModule(name, "2.0.0", map[string]map[string]string{"en": {"changed": "updated", "added": "new"}})
	publish := func(mod *module.LoadedModule) error {
		_, err := reg.UpdateWithContext(t.Context(), func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
			next := maps.Clone(current)
			next[name] = mod
			return next, nil
		})
		return err
	}

	var readers sync.WaitGroup
	readers.Go(func() {
		for range 1000 {
			value, ok := reg.Translations.Lookup("en", name+".changed")
			if !ok || (value != "original" && value != "updated") {
				t.Errorf("concurrent lookup = %q, %v", value, ok)
			}
		}
	})
	if err := publish(upgraded); err != nil {
		t.Fatal(err)
	}
	readers.Wait()

	// Followers receive compiled code separately and refresh translations from shared storage.
	follower := &ModuleRegistry{Translations: translationstore.New(conn)}
	if _, err := follower.UpdateWithContext(t.Context(), func(map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		return map[string]*module.LoadedModule{name: translatedModule(name, "2.0.0", nil)}, nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, store := range []*translationstore.Store{reg.Translations, follower.Translations} {
		for key, want := range map[string]string{"changed": "updated", "kept": "retained", "added": "new", "empty": ""} {
			if got, ok := store.Lookup("en", name+"."+key); !ok || got != want {
				t.Fatalf("%s = %q, %v; want %q", key, got, ok, want)
			}
		}

		if got, ok := store.Lookup("fr", name+".kept"); !ok || got != "conservé" {
			t.Fatalf("omitted locale = %q, %v", got, ok)
		}
	}

	if err := publish(translatedModule(name, "3.0.0", nil)); err != nil {
		t.Fatal(err)
	}

	if _, ok := reg.Translations.Lookup("en", name+".kept"); !ok {
		t.Fatal("translation-free upgrade deleted key")
	}

	if _, err := reg.UninstallModule(t.Context(), nil, name, nil, nil); err != nil {
		t.Fatal(err)
	}

	if _, ok := reg.Translations.Lookup("en", name+".kept"); ok {
		t.Fatal("uninstalled key remains cached")
	}

	if got, ok := reg.Translations.Lookup("en", other+".changed"); !ok || got != "other" {
		t.Fatalf("other namespace = %q, %v", got, ok)
	}

	if err := conn.QueryRow(`SELECT COUNT(*) FROM system.module_translations WHERE module_name = $1`, name).Scan(&count); err != nil || count != 0 {
		t.Fatalf("uninstall rows = %d, error %v", count, err)
	}
}

func TestBackendTranslationsFailedPublicationAndOfflineLookup(t *testing.T) {
	reg, conn, name := translationRegistry(t)
	original := translatedModule(name, "1.0.0", map[string]map[string]string{"en": {"a": "original"}})
	modules := map[string]*module.LoadedModule{name: original}
	if _, err := reg.UpdateWithContext(t.Context(), func(map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) { return modules, nil }); err != nil {
		t.Fatal(err)
	}

	before := reg.Snapshot()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := reg.UpdateWithContext(ctx, func(map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		return map[string]*module.LoadedModule{name: translatedModule(name, "2.0.0", map[string]map[string]string{"en": {"a": "rejected"}})}, nil
	}); err == nil {
		t.Fatal("cancelled publication succeeded")
	}

	if reg.Snapshot() != before {
		t.Fatal("failed translation write published module")
	}

	var stored string
	if err := conn.QueryRow(`SELECT translation FROM system.module_translations WHERE module_name=$1 AND locale='en' AND translation_key=$2`, name, name+".a").Scan(&stored); err != nil || stored != "original" {
		t.Fatalf("stored value = %q, error %v", stored, err)
	}

	offlineConn, err := db.New("postgres://goerp:dev@localhost:15432/goerp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = offlineConn.Close() })
	offline := translationstore.New(offlineConn)
	if err := offline.Apply(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}

	if err := offlineConn.Close(); err != nil {
		t.Fatal(err)
	}

	if got, ok := offline.Lookup("en", name+".a"); !ok || got != "original" {
		t.Fatalf("offline lookup = %q, %v", got, ok)
	}

	if _, ok := offline.Lookup("de", name+".a"); ok {
		t.Fatal("exact lookup unexpectedly applied fallback")
	}
}

func TestBackendTranslationsMetadataCopyPreservesSharedUpdates(t *testing.T) {
	reg, conn, name := translationRegistry(t)
	original := translatedModule(name, "1.0.0", map[string]map[string]string{"en": {"a": "original"}})
	if _, err := reg.UpdateWithContext(t.Context(), func(map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		return map[string]*module.LoadedModule{name: original}, nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := translationstore.New(conn).Apply(t.Context(), map[string]map[string]map[string]string{name: {"en": {"a": "shared update"}}}, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.UpdateWithContext(t.Context(), func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		copyModule := *current[name]
		return map[string]*module.LoadedModule{name: &copyModule}, nil
	}); err != nil {
		t.Fatal(err)
	}

	if value, ok := reg.Translations.Lookup("en", name+".a"); !ok || value != "shared update" {
		t.Fatalf("metadata publication overwrote shared translation: %q, %v", value, ok)
	}
}
