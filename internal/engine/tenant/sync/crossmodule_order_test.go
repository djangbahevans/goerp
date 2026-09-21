package tenantsync

import (
	"context"
	"fmt"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// Each module in a chain sorts before the module it depends on, so a sync in
// name order, or all at once, reaches a foreign key before the table it
// references exists.
func TestSyncWorker_Run_SyncsDependencyBeforeDependent(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	const chain = 6
	name := func(i int) string { return fmt.Sprintf("m%d%s", i, slug) }
	mods := make(map[string]*module.LoadedModule, chain)
	for i := range chain {
		decl := model.Define("item", model.Table(fmt.Sprintf("items%d", i))).
			Field("id", model.UUID().Required().PrimaryKey())
		if i < chain-1 {
			decl = decl.Field("parent_id", model.Many2One(name(i+1)+".item"))
		}
		mod := loadedModule(t, name(i), *decl)
		if i < chain-1 {
			mod.Manifest.DependsOn = []string{name(i + 1)}
		}
		mods[name(i)] = mod
	}

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(mods); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	diffEngine := schema.NewSchemaDiffEngine(&schema.Config{ModelSource: reg})

	w := &SyncWorker{TenantStore: env.tenantStore, Registry: reg, Pool: env.pool, DiffEngine: diffEngine}
	result, err := w.run(context.Background(), SyncArgs{TenantSlug: slug})
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("Failed = %+v, want none", result.Failed)
	}
	if len(result.Synced) != chain {
		t.Fatalf("Synced = %d modules, want %d", len(result.Synced), chain)
	}

	var foreignKeys int
	if err := env.conn.QueryRow(`SELECT count(*) FROM information_schema.table_constraints WHERE table_schema = $1 AND constraint_type = 'FOREIGN KEY'`, "tenant_"+slug).Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign keys: %v", err)
	}
	if foreignKeys != chain-1 {
		t.Errorf("foreign key count = %d, want %d", foreignKeys, chain-1)
	}
}
