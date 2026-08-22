package registry

import (
	"strings"
	"sync"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestModuleRegistry_Snapshot_NilBeforeFirstUpdate(t *testing.T) {
	r := &ModuleRegistry{}

	if got := r.Snapshot(); got != nil {
		t.Fatalf("Snapshot() = %v, want nil before any Update", got)
	}
}

func TestModuleRegistry_Update_PublishesNewSnapshot(t *testing.T) {
	r := &ModuleRegistry{}
	modules := map[string]*module.LoadedModule{
		"contacts": {Manifest: manifest.Manifest{Type: "standard"}},
	}

	snap, err := r.Update(modules)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got := r.Snapshot(); got != snap {
		t.Fatalf("Snapshot() = %p, want the snapshot returned by Update (%p)", got, snap)
	}
}

func TestModuleRegistry_Update_SnapshotExposesPermissionRegistry(t *testing.T) {
	r := &ModuleRegistry{}
	modules := map[string]*module.LoadedModule{
		"contacts": {
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Type: "standard", Permissions: []manifest.Permission{{Name: "contacts:contact:read"}}},
		},
	}

	snap, err := r.Update(modules)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	reg := snap.PermissionRegistry()
	if reg == nil {
		t.Fatal("PermissionRegistry() = nil")
	}
	if _, ok := reg.Index("contacts:contact:read"); !ok {
		t.Error("expected the module's declared permission to be registered")
	}
}

func TestModuleRegistry_Update_CarriesOverCronSchemaRegistries(t *testing.T) {
	r := &ModuleRegistry{}

	snap1, err := r.Update(map[string]*module.LoadedModule{
		"contacts": {Manifest: manifest.Manifest{Type: "standard"}},
	})
	if err != nil {
		t.Fatalf("first Update() error = %v", err)
	}

	snap2, err := r.Update(map[string]*module.LoadedModule{
		"billing": {Manifest: manifest.Manifest{Type: "standard"}},
	})
	if err != nil {
		t.Fatalf("second Update() error = %v", err)
	}

	if snap2.cronRegistry != snap1.cronRegistry {
		t.Errorf("cronRegistry was rebuilt, want carried over unchanged")
	}
	if snap2.schemaRegistry != snap1.schemaRegistry {
		t.Errorf("schemaRegistry was rebuilt, want carried over unchanged")
	}

	if snap2.routeTable == snap1.routeTable {
		t.Errorf("routeTable was carried over, want a fresh rebuild")
	}
	if snap2.eventRegistry == snap1.eventRegistry {
		t.Errorf("eventRegistry was carried over, want a fresh rebuild")
	}
	if snap2.permRegistry == snap1.permRegistry {
		t.Errorf("permRegistry was carried over, want a fresh rebuild")
	}
	if snap2.fieldSecRegistry == snap1.fieldSecRegistry {
		t.Errorf("fieldSecRegistry was carried over, want a fresh rebuild")
	}
	if snap2.jobRegistry == snap1.jobRegistry {
		t.Errorf("jobRegistry was carried over, want a fresh rebuild")
	}
}

func TestModuleRegistry_Update_JobTypeCollisionAcrossModulesFails(t *testing.T) {
	r := &ModuleRegistry{}

	_, err := r.Update(map[string]*module.LoadedModule{
		"billing": {Manifest: manifest.Manifest{Type: "standard", JobTypes: []manifest.JobType{
			{Name: "send_invoice", Label: "Send Invoice", Handler: "send_invoice", Queue: "default"},
		}}},
		"contacts": {Manifest: manifest.Manifest{Type: "standard", JobTypes: []manifest.JobType{
			{Name: "send_invoice", Label: "Send Invoice (dupe)", Handler: "send_invoice", Queue: "default"},
		}}},
	})
	if err == nil {
		t.Fatal("Update() with two modules declaring the same job type name: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "send_invoice") {
		t.Errorf("Update() error = %q, want it to mention the colliding job type name", err.Error())
	}
}

func TestModuleRegistry_Update_JobTypesRecordedInSnapshot(t *testing.T) {
	r := &ModuleRegistry{}

	snap, err := r.Update(map[string]*module.LoadedModule{
		"billing": {Manifest: manifest.Manifest{Type: "standard", JobTypes: []manifest.JobType{
			{Name: "send_invoice", Label: "Send Invoice", Handler: "send_invoice", Queue: "default"},
		}}},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	owner, ok := snap.jobRegistry.Owner("send_invoice")
	if !ok {
		t.Fatal("expected \"send_invoice\" to be registered")
	}
	if owner != "billing" {
		t.Errorf("Owner(\"send_invoice\") = %q, want %q", owner, "billing")
	}
}

func TestModuleRegistry_Update_SkipsFailedModulesJobTypes(t *testing.T) {
	r := &ModuleRegistry{}

	snap, err := r.Update(map[string]*module.LoadedModule{
		"billing": func() *module.LoadedModule {
			m := &module.LoadedModule{Manifest: manifest.Manifest{Type: "standard", JobTypes: []manifest.JobType{
				{Name: "send_invoice", Label: "Send Invoice", Handler: "send_invoice", Queue: "default"},
			}}}
			m.Fail("unrelated load failure")
			return m
		}(),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if _, ok := snap.jobRegistry.Owner("send_invoice"); ok {
		t.Error("expected a StatusFailed module's job types not to be registered")
	}
}

func TestModuleRegistry_Update_InFlightReaderSeesStableSnapshot(t *testing.T) {
	r := &ModuleRegistry{}

	s1, err := r.Update(map[string]*module.LoadedModule{
		"contacts": {Manifest: manifest.Manifest{Type: "standard"}},
	})
	if err != nil {
		t.Fatalf("first Update() error = %v", err)
	}
	reader := r.Snapshot()
	if reader != s1 {
		t.Fatalf("Snapshot() before second Update = %p, want %p", reader, s1)
	}

	s2, err := r.Update(map[string]*module.LoadedModule{
		"billing": {Manifest: manifest.Manifest{Type: "standard"}},
	})
	if err != nil {
		t.Fatalf("second Update() error = %v", err)
	}

	if reader != s1 {
		t.Fatalf("previously read snapshot changed after a later Update; got %p, want stable %p", reader, s1)
	}
	if got := r.Snapshot(); got != s2 {
		t.Fatalf("Snapshot() after second Update = %p, want %p", got, s2)
	}
}

func TestModuleRegistry_Update_ConcurrentWritersSerialize(t *testing.T) {
	r := &ModuleRegistry{}
	const writers = 20

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := range writers {
		go func(i int) {
			defer wg.Done()
			name := "module" + string(rune('a'+i))
			_, err := r.Update(map[string]*module.LoadedModule{
				name: {Manifest: manifest.Manifest{Type: "standard"}},
			})
			if err != nil {
				t.Errorf("Update() error = %v", err)
			}
		}(i)
	}
	wg.Wait()

	final := r.Snapshot()
	if final == nil {
		t.Fatal("Snapshot() = nil after concurrent updates")
	}
	if len(final.modules) != 1 {
		t.Fatalf("final snapshot has %d modules, want exactly 1 (one writer's input, not a merge)", len(final.modules))
	}
}

func TestModuleRegistry_Update_RouteConflict_ReturnsErrorWithoutPublishing(t *testing.T) {
	r := &ModuleRegistry{}
	first, err := r.Update(map[string]*module.LoadedModule{
		"contacts": {Manifest: manifest.Manifest{Type: "standard"}},
	})
	if err != nil {
		t.Fatalf("first Update() error = %v", err)
	}

	_, err = r.Update(map[string]*module.LoadedModule{
		"auth": {
			Manifest: manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{
				{Method: "GET", Path: "/"}, // expands under the reserved "auth" namespace
			},
		},
	})
	if err == nil {
		t.Fatal("expected an error for a route registered under a reserved namespace")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("error = %q, want it to mention the reserved namespace", err.Error())
	}

	if got := r.Snapshot(); got != first {
		t.Fatalf("Snapshot() changed after a failed Update; got %p, want the prior snapshot %p", got, first)
	}
}

func TestModuleRegistry_Snapshot_ReflectsInPlaceStatusMutation(t *testing.T) {
	r := &ModuleRegistry{}
	m := &module.LoadedModule{Status: module.StatusSyncing, Manifest: manifest.Manifest{Type: "standard"}}

	snap, err := r.Update(map[string]*module.LoadedModule{"widgets": m})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// A later stage (e.g. poolwarm.WarmAll) mutates the same *LoadedModule
	// pointer directly, without calling Update again.
	m.Status = module.StatusReady

	if got := snap.Modules()["widgets"].Status; got != module.StatusReady {
		t.Fatalf("already-published snapshot's Status = %v, want StatusReady — Modules() must return the same pointers Update was given, not copies", got)
	}
}

func TestModuleRegistry_Update_SkipsFailedModuleEvenWithConflictingRoutes(t *testing.T) {
	r := &ModuleRegistry{}

	// "auth" is StatusFailed and would conflict with the reserved "auth"
	// namespace if it were registered — exactly the situation a module
	// that failed its own route registration during loading is in.
	// Update must not re-trigger that conflict for a module already
	// marked failed.
	_, err := r.Update(map[string]*module.LoadedModule{
		"contacts": {Manifest: manifest.Manifest{Type: "standard"}},
		"auth": {
			Status:   module.StatusFailed,
			Manifest: manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{
				{Method: "GET", Path: "/"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v, want a StatusFailed module's routes to be skipped, not registered", err)
	}
}

func TestBuildRouteTable_FromModules(t *testing.T) {
	modules := map[string]*module.LoadedModule{
		"contacts": {
			Manifest: manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{
				{Method: "GET", Path: "/ping"},
			},
		},
	}

	table, err := buildRouteTable(modules)
	if err != nil {
		t.Fatalf("buildRouteTable() error = %v", err)
	}

	entry, _, result, _ := table.Lookup("GET", "/contacts/ping")
	if result != route.RouteFound {
		t.Fatalf("Lookup() result = %v, want RouteFound", result)
	}
	if entry.ModuleName != "contacts" {
		t.Fatalf("entry.ModuleName = %q, want %q", entry.ModuleName, "contacts")
	}
}

// TestBuildRouteTable_IncludesBuiltinRoutes guards goerp#86: /_health,
// /_ready, and POST /auth/login must resolve through the same RouteTable
// module routes do, not a second router — and survive every rebuild
// (module load, hot reload), not just an initial registration step that
// could fall out of sync.
func TestBuildRouteTable_IncludesBuiltinRoutes(t *testing.T) {
	modules := map[string]*module.LoadedModule{
		"contacts": {Manifest: manifest.Manifest{Type: "standard"}},
	}

	table, err := buildRouteTable(modules)
	if err != nil {
		t.Fatalf("buildRouteTable() error = %v", err)
	}

	for _, path := range []string{"/_health", "/_ready"} {
		entry, _, result, _ := table.Lookup("GET", path)
		if result != route.RouteFound {
			t.Fatalf("Lookup(GET, %q) result = %v, want RouteFound", path, result)
		}
		if !entry.Manifest.EngineNative {
			t.Errorf("Lookup(GET, %q).Manifest.EngineNative = false, want true", path)
		}
	}

	entry, _, result, _ := table.Lookup("POST", "/auth/login")
	if result != route.RouteFound {
		t.Fatalf("Lookup(POST, /auth/login) result = %v, want RouteFound", result)
	}
	if !entry.Manifest.EngineNative {
		t.Error("Lookup(POST, /auth/login).Manifest.EngineNative = false, want true")
	}
}

func TestBuildEventRegistry_FromModules(t *testing.T) {
	modules := map[string]*module.LoadedModule{
		"billing": {
			Manifest: manifest.Manifest{
				Emits: []manifest.EventDeclaration{{Name: "invoice.created", Version: 1}},
			},
		},
	}

	reg := buildEventRegistry(modules)

	emitters := reg.Emitters("invoice.created")
	if len(emitters) != 1 || emitters[0] != "billing" {
		t.Fatalf("Emitters(%q) = %v, want [billing]", "invoice.created", emitters)
	}
}

func TestBuildPermissionRegistry_FromModules(t *testing.T) {
	modules := map[string]*module.LoadedModule{
		"billing": {
			Manifest: manifest.Manifest{
				Permissions: []manifest.Permission{{Name: "billing.view_invoices"}},
			},
		},
	}

	reg := buildPermissionRegistry(modules)

	if _, ok := reg.Index("billing.view_invoices"); !ok {
		t.Fatalf("expected an index for billing.view_invoices")
	}
}

func TestBuildFieldSecRegistry_FromModules(t *testing.T) {
	modules := map[string]*module.LoadedModule{
		"contacts": {
			ModelDecls: []model.ModelDeclaration{
				{Name: "contact", Fields: []model.NamedField{{Name: "ssn"}}},
			},
		},
	}

	reg := buildFieldSecRegistry(modules)

	// FieldDef carries no security data until SDK backlog #19 lands, so no
	// rule is expected yet — this only exercises that the wiring reaches
	// fieldsec.Register without panicking or mis-keying.
	if _, ok := reg.Rule("contacts.contact", "ssn"); ok {
		t.Fatalf("expected no rule for contacts.contact.ssn until SDK backlog #19 lands")
	}
}
