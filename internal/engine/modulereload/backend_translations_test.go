package modulereload

import (
	"context"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/l10n/translationstore"
)

func TestLeaderRunMergesBackendTranslations(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)
	name := "widgets_" + slug
	leader, reg := newLeader(t, env, nil)
	store := translationstore.New(env.conn)
	if err := store.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = env.conn.Exec(`DELETE FROM system.module_translations WHERE module_name=$1`, name) })
	reg.Translations = store
	source, mf := buildSource(t, name, "1.0.0", compileFixture(t, ""), nil)
	source.BackendTranslations = map[string][]byte{"en": []byte(`{"changed":"original","kept":"retained"}`)}
	if err := leader.Run(t.Context(), name, source, mf); err != nil {
		t.Fatal(err)
	}

	old := reg.Snapshot().Modules()[name]
	t.Cleanup(func() {
		if err := old.Pool.DrainAndClose(context.Background(), 5*time.Second); err != nil {
			t.Error(err)
		}
		_ = old.CompiledModule.Close(context.Background())
	})
	source, mf = buildSource(t, name, "1.1.0", compileFixture(t, "1"), nil)
	source.BackendTranslations = map[string][]byte{"en": []byte(`{"changed":"updated","added":"new"}`)}
	if err := leader.Run(t.Context(), name, source, mf); err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string]string{"changed": "updated", "kept": "retained", "added": "new"} {
		if got, ok := store.Lookup("en", name+"."+key); !ok || got != want {
			t.Fatalf("%s = %q, %v", key, got, ok)
		}
	}
}
