package moduleinstall

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/l10n/translationstore"
)

func TestWorkerRunStoresBackendTranslations(t *testing.T) {
	env := newTestEnv(t)
	name := "widgets_" + uniqueSlug(t)
	store := translationstore.New(env.conn)
	if err := store.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = env.conn.Exec(`DELETE FROM system.module_translations WHERE module_name=$1`, name) })
	worker, reg := newWorker(t, env, nil)
	reg.Translations = store
	pkg := buildPackageWithMembers(t, name, compileFixture(t), nil, map[string][]byte{
		"translations/en.json": []byte(`{"a":"Name"}`),
		"translations/fr.json": []byte(`{"a":"Nom"}`),
	})
	if _, err := worker.run(t.Context(), Args{PackagePath: writeTempPackage(t, pkg)}); err != nil {
		t.Fatal(err)
	}

	for locale, want := range map[string]string{"en": "Name", "fr": "Nom"} {
		if got, ok := store.Lookup(locale, name+".a"); !ok || got != want {
			t.Fatalf("%s lookup = %q, %v", locale, got, ok)
		}
	}
}
