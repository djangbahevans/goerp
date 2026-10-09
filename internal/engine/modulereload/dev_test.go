package modulereload

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/tetratelabs/wazero"
)

type devCompiledModule struct {
	wazero.CompiledModule
	onClose func()
}

func (m *devCompiledModule) Close(ctx context.Context) error {
	err := m.CompiledModule.Close(ctx)
	m.onClose()

	return err
}

func TestDevelopmentReloadSyncsSameVersionOnlyForSelectedTenant(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)
	other := uniqueSlug(t)
	env.activeTenant(t, other)
	name := "widgets_" + slug
	l, reg := newLeader(t, env, nil)
	l.DevTenant = slug

	src, mf := buildSource(t, name, "1.0.0", compileFixture(t, ""), nil)
	if err := l.Run(t.Context(), name, src, mf); err != nil {
		t.Fatal(err)
	}
	old := reg.Snapshot().Modules()[name]
	closed := make(chan struct{})
	observed := *old
	observed.CompiledModule = &devCompiledModule{
		CompiledModule: old.CompiledModule,
		onClose:        sync.OnceFunc(func() { close(closed) }),
	}
	if _, err := reg.Update(map[string]*module.LoadedModule{name: new(observed)}); err != nil {
		t.Fatal(err)
	}
	old = reg.Snapshot().Modules()[name]

	if _, err := env.conn.ExecContext(t.Context(), "INSERT INTO "+quoteIdent("tenant_"+slug)+".widgets_widget (tenant_id, name) VALUES ($1, 'preserved')", envTenantID(t, env, slug)); err != nil {
		t.Fatal(err)
	}

	src, mf = buildSource(t, name, "1.0.0", compileFixture(t, "1"), nil)
	if err := l.Run(t.Context(), name, src, mf); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Error("superseded module cleanup did not finish before runtime shutdown")
		}
	})

	if !columnExists(t, env.conn, "tenant_"+slug, "widgets_widget", "extra") {
		t.Fatal("same-version reload did not add the declared column")
	}
	if tableExists(t, env.conn, "tenant_"+other, "widgets_widget") {
		t.Fatal("development reload synchronized an unrelated tenant")
	}
	if reg.Snapshot().Modules()[name] == old {
		t.Fatal("same-version reload retained the old module")
	}

	var count int
	if err := env.conn.QueryRowContext(t.Context(), "SELECT count(*) FROM "+quoteIdent("tenant_"+slug)+".widgets_widget WHERE name = 'preserved'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("reload lost existing data: %d, %v", count, err)
	}
}

func envTenantID(t *testing.T, env *testEnv, slug string) string {
	t.Helper()
	tenant, err := env.tenantStore.GetBySlug(t.Context(), slug)
	if err != nil {
		t.Fatal(err)
	}
	return tenant.ID
}
