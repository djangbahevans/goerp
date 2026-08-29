package tenantsync

import (
	"context"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
)

func TestSyncWorker_Run_SyncsExplicitTenantAndModule(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	modName := "widgets_" + slug
	mod := loadedModule(t, modName, widgetModel())
	reg := newTestRegistry(t, mod)

	w := &SyncWorker{TenantStore: env.tenantStore, Registry: reg, Pool: env.pool, DiffEngine: env.diffEngine}
	result, err := w.run(context.Background(), SyncArgs{TenantSlug: slug, ModuleName: modName})
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if len(result.Failed) != 0 {
		t.Errorf("Failed = %+v, want none", result.Failed)
	}
	if len(result.Synced) != 1 || result.Synced[0].Tenant != slug || result.Synced[0].Module != modName {
		t.Errorf("Synced = %+v, want exactly {%s, %s}", result.Synced, slug, modName)
	}
	if !tableExists(t, env.conn, "tenant_"+slug, "widgets") {
		t.Error("expected the widgets table to have been created")
	}
}

func TestSyncWorker_Run_UnknownModuleErrors(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{}); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	w := &SyncWorker{TenantStore: env.tenantStore, Registry: reg, Pool: env.pool, DiffEngine: env.diffEngine}

	_, err := w.run(context.Background(), SyncArgs{TenantSlug: slug, ModuleName: "does-not-exist"})
	if err == nil {
		t.Fatal("run() error = nil, want an error for an unknown module")
	}
}
