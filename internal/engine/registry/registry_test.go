package registry

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

	if snap2.cronRegistry == snap1.cronRegistry {
		t.Errorf("cronRegistry was carried over, want a fresh rebuild")
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

// TestModuleRegistry_UpdateWith_ConcurrentWritersMergeWithoutLosingUpdates
// is UpdateWith's own version of TestModuleRegistry_Update_ConcurrentWritersSerialize
// above — but where plain Update's own contract is "whichever caller runs
// last wins wholesale, callers merge for themselves," UpdateWith exists
// specifically so a caller's own merge (read current, add its module,
// return the merged map) runs with writeMu already held. Two writer kinds
// (install, hot reload) built exactly that shape independently on top of
// plain Update before goerp#467 — read Snapshot(), clone+merge, then
// Update(merged) — and it silently lost whichever update published second,
// since the second caller's clone was built from a snapshot that didn't
// yet include the first caller's just-published module. This test is that
// scenario, fixed: every one of N concurrent writers must survive in the
// final snapshot.
func TestModuleRegistry_UpdateWith_ConcurrentWritersMergeWithoutLosingUpdates(t *testing.T) {
	r := &ModuleRegistry{}
	const writers = 20

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := range writers {
		go func(i int) {
			defer wg.Done()
			name := "module" + string(rune('a'+i))
			_, err := r.UpdateWith(func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
				merged := make(map[string]*module.LoadedModule, len(current)+1)
				maps.Copy(merged, current)
				merged[name] = &module.LoadedModule{Manifest: manifest.Manifest{Type: "standard"}}
				return merged, nil
			})
			if err != nil {
				t.Errorf("UpdateWith() error = %v", err)
			}
		}(i)
	}
	wg.Wait()

	final := r.Snapshot()
	if final == nil {
		t.Fatal("Snapshot() = nil after concurrent updates")
	}
	if len(final.modules) != writers {
		t.Errorf("final snapshot has %d modules, want all %d writers' modules present (no lost update)", len(final.modules), writers)
	}
}

// TestModuleRegistry_Reserve_SecondCallerForSameNameFails is the
// reservation-stage half of the same goerp#467 fix: install and hot reload
// used to each keep their own private "names in progress" set, so an
// install and a hot reload of the identical module name could both pass
// their own reservation check and run a full, wasted compile/sync pipeline
// before UpdateWith's merge ever caught the conflict. Reserve is the one
// shared gate both now go through first.
func TestModuleRegistry_Reserve_SecondCallerForSameNameFails(t *testing.T) {
	r := &ModuleRegistry{}

	release, err := r.Reserve("widgets")
	if err != nil {
		t.Fatalf("first Reserve() error = %v", err)
	}

	if _, err := r.Reserve("widgets"); !errors.Is(err, ErrReserved) {
		t.Errorf("second Reserve() for the same name error = %v, want ErrReserved", err)
	}

	if _, err := r.Reserve("gadgets"); err != nil {
		t.Errorf("Reserve() for a different name error = %v, want nil", err)
	}

	release()

	if _, err := r.Reserve("widgets"); err != nil {
		t.Errorf("Reserve() after release error = %v, want nil", err)
	}
}

// TestModuleRegistry_LockUpdateWithLocked_SerializesPublishPlusFollowUpStep
// exercises the exact shape moduleinstall.Worker.publish and
// modulereload.Leader.publish both use: Lock, UpdateWithLocked, some
// further "rebuild a derived cache" step, Unlock — proving that step is
// genuinely atomic with the publish across two concurrent callers, not
// just the publish itself. Each goroutine's "step" records the module
// count its own newSnap saw into a shared, unsynchronized-by-design
// variable (protected only by the same Lock the real callers use); if
// Lock didn't cover the step too, the two steps could interleave and the
// final recorded count could reflect whichever finished last rather than
// whichever published last. Since UpdateWithLocked always merges against
// the true current map, the writer that publishes second always sees both
// modules — so if the lock is doing its job, the final recorded count is
// always 2, deterministically, on every run.
func TestModuleRegistry_LockUpdateWithLocked_SerializesPublishPlusFollowUpStep(t *testing.T) {
	r := &ModuleRegistry{}

	var lastRebuiltCount int
	publishAndRebuild := func(name string) {
		r.Lock()
		defer r.Unlock()

		newSnap, err := r.UpdateWithLocked(func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
			merged := make(map[string]*module.LoadedModule, len(current)+1)
			maps.Copy(merged, current)
			merged[name] = &module.LoadedModule{Manifest: manifest.Manifest{Type: "standard"}}
			return merged, nil
		})
		if err != nil {
			t.Errorf("UpdateWithLocked() error = %v", err)
			return
		}

		// Simulates RebuildAll's own slow, several-tenant DB work — long
		// enough that, without the lock covering this step too, the other
		// goroutine's own step would very likely interleave with it.
		time.Sleep(10 * time.Millisecond)
		lastRebuiltCount = len(newSnap.modules)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); publishAndRebuild("a") }()
	go func() { defer wg.Done(); publishAndRebuild("b") }()
	wg.Wait()

	if lastRebuiltCount != 2 {
		t.Errorf("lastRebuiltCount = %d, want 2 (whichever writer published second must also run its own step last, atomically)", lastRebuiltCount)
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

func TestModuleRegistry_Update_SnapshotSchemaHashChangesOnRouteChange(t *testing.T) {
	r := &ModuleRegistry{}
	snap1, err := r.Update(map[string]*module.LoadedModule{
		"contacts": {
			Status:         module.StatusReady,
			Manifest:       manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{{Method: "GET", Path: "/", Auth: "required"}},
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if snap1.SchemaHash() == "" {
		t.Fatal("SchemaHash() is empty")
	}

	snap2, err := r.Update(map[string]*module.LoadedModule{
		"contacts": {
			Status:         module.StatusReady,
			Manifest:       manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{{Method: "GET", Path: "/other", Auth: "required"}},
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if snap1.SchemaHash() == snap2.SchemaHash() {
		t.Errorf("SchemaHash() unchanged (%s) after a route changed", snap1.SchemaHash())
	}
}

func TestModuleRegistry_Update_SnapshotSchemaHashChangesOnNameChange(t *testing.T) {
	// Same Method/Path/Model/CrudAction/ResponseIsList — only Name differs,
	// e.g. an EnableOps-auto-generated "get" route (Name == "") overridden
	// by a hand-registered engine.DefineAction[M, NoBody](engine.Get) (Name ==
	// "get"). The hash must still change, or a cached /_meta/schema
	// response never learns the action became name-resolvable.
	r := &ModuleRegistry{}
	snap1, err := r.Update(map[string]*module.LoadedModule{
		"contacts": {
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{
				{Method: "GET", Path: "/contacts/{id}", Auth: "required", Model: "contacts.contact", CRUDAction: "get"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	snap2, err := r.Update(map[string]*module.LoadedModule{
		"contacts": {
			Status:     module.StatusReady,
			Manifest:   manifest.Manifest{Type: "standard"},
			ModelDecls: []model.ModelDeclaration{*model.Define("contact")},
			ExplicitRoutes: []engine.RouteDeclaration{
				{Auth: "required", Model: "contacts.contact", CRUDAction: "get", Name: "get"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if snap1.SchemaHash() == snap2.SchemaHash() {
		t.Errorf("SchemaHash() unchanged (%s) after Name changed", snap1.SchemaHash())
	}
}

func TestModuleRegistry_Update_SnapshotSchemaHashChangesOnRouteTypeChange(t *testing.T) {
	// A field added to an engine.Returns type must change the hash, or
	// goerp codegen --watch keeps the old generated types.
	hashWith := func(t *testing.T, responseType *engine.TypeDesc) string {
		t.Helper()
		snap, err := (&ModuleRegistry{}).Update(map[string]*module.LoadedModule{
			"contacts": {
				Status:   module.StatusReady,
				Manifest: manifest.Manifest{Type: "standard"},
				ExplicitRoutes: []engine.RouteDeclaration{
					{Method: "GET", Path: "/export", Auth: "required", ResponseType: responseType},
				},
			},
		})
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		return snap.SchemaHash()
	}

	untyped := hashWith(t, nil)
	typed := hashWith(t, &engine.TypeDesc{Kind: "object", Name: "Export", Fields: []engine.FieldDesc{
		{Name: "id", Type: engine.TypeDesc{Kind: "string"}},
	}})
	widened := hashWith(t, &engine.TypeDesc{Kind: "object", Name: "Export", Fields: []engine.FieldDesc{
		{Name: "id", Type: engine.TypeDesc{Kind: "string"}},
		{Name: "total", Type: engine.TypeDesc{Kind: "number"}},
	}})
	if untyped == typed || typed == widened {
		t.Errorf("SchemaHash() untyped=%s typed=%s widened=%s, want all different", untyped, typed, widened)
	}
}

func TestModuleRegistry_Update_SnapshotSchemaHashStableAcrossIdenticalRebuilds(t *testing.T) {
	buildModules := func() map[string]*module.LoadedModule {
		return map[string]*module.LoadedModule{
			"contacts": {
				Status:         module.StatusReady,
				Manifest:       manifest.Manifest{Type: "standard"},
				ExplicitRoutes: []engine.RouteDeclaration{{Method: "GET", Path: "/", Auth: "required"}},
			},
		}
	}

	r1 := &ModuleRegistry{}
	snap1, err := r1.Update(buildModules())
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	r2 := &ModuleRegistry{}
	snap2, err := r2.Update(buildModules())
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if snap1.SchemaHash() != snap2.SchemaHash() {
		t.Errorf("SchemaHash() differs across two registries built from identical modules: %s vs %s", snap1.SchemaHash(), snap2.SchemaHash())
	}
}

func TestModuleRegistry_Update_SnapshotSchemaHashIgnoresFailedModules(t *testing.T) {
	r := &ModuleRegistry{}
	snap1, err := r.Update(map[string]*module.LoadedModule{
		"contacts": {
			Status:         module.StatusReady,
			Manifest:       manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{{Method: "GET", Path: "/", Auth: "required"}},
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	snap2, err := r.Update(map[string]*module.LoadedModule{
		"contacts": {
			Status:         module.StatusReady,
			Manifest:       manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{{Method: "GET", Path: "/", Auth: "required"}},
		},
		"broken": {Status: module.StatusFailed, Manifest: manifest.Manifest{Type: "standard"}},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if snap1.SchemaHash() != snap2.SchemaHash() {
		t.Errorf("SchemaHash() changed after adding only a StatusFailed module: %s vs %s", snap1.SchemaHash(), snap2.SchemaHash())
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
		if !entry.Manifest.EngineBuiltin {
			t.Errorf("Lookup(GET, %q).Manifest.EngineBuiltin = false, want true", path)
		}
	}

	entry, _, result, _ := table.Lookup("POST", "/auth/login")
	if result != route.RouteFound {
		t.Fatalf("Lookup(POST, /auth/login) result = %v, want RouteFound", result)
	}
	if !entry.Manifest.EngineNative {
		t.Error("Lookup(POST, /auth/login).Manifest.EngineNative = false, want true")
	}
	if !entry.Manifest.EngineBuiltin {
		t.Error("Lookup(POST, /auth/login).Manifest.EngineBuiltin = false, want true")
	}

	uploadEntry, _, uploadResult, _ := table.Lookup("POST", "/storage/upload")
	if uploadResult != route.RouteFound {
		t.Fatalf("Lookup(POST, /storage/upload) result = %v, want RouteFound", uploadResult)
	}
	if !uploadEntry.Manifest.EngineNative {
		t.Error("Lookup(POST, /storage/upload).Manifest.EngineNative = false, want true")
	}
	if !uploadEntry.Manifest.EngineBuiltin {
		t.Error("Lookup(POST, /storage/upload).Manifest.EngineBuiltin = false, want true")
	}

	// A route engine.go's builtinRoutes dispatches is unreachable in
	// production unless it also resolves here.
	for _, c := range []struct{ method, path string }{
		{"GET", "/auth/me"},
		{"POST", "/auth/mfa/enroll/totp"},
		{"POST", "/auth/mfa/enroll/totp/confirm"},
		{"POST", "/auth/mfa/enroll/webauthn"},
		{"POST", "/auth/mfa/enroll/webauthn/confirm"},
		{"POST", "/auth/mfa/webauthn/options"},
		{"POST", "/auth/mfa/reverify/webauthn/options"},
		{"GET", "/auth/mfa/factors"},
		{"POST", "/auth/mfa/factors/0197a4f2-0000-7000-8000-000000000000/remove"},
		{"POST", "/auth/mfa/recovery-codes/regenerate"},
		{"GET", "/auth/tenant-context"},
		{"POST", "/auth/refresh"},
		{"POST", "/auth/logout"},
		{"POST", "/admin/tenant/plan"},
		{"POST", "/auth/password-reset/request"},
		{"POST", "/auth/password-reset/confirm"},
		{"POST", "/auth/verify-email"},
		{"POST", "/auth/verify-email/resend"},
		{"POST", "/auth/me/change-password"},
		{"POST", "/auth/accept-invite"},
		{"GET", "/auth/accept-invite/info"},
		{"POST", "/auth/register"},
		{"GET", "/auth/check-slug"},
		{"GET", "/admin/settings"},
		{"PATCH", "/admin/settings"},
		{"POST", "/admin/settings/logo"},
		{"DELETE", "/admin/settings/logo"},
		{"GET", "/admin/settings/notification-delivery"},
		{"PATCH", "/admin/settings/notification-delivery"},
		{"POST", "/admin/settings/notification-delivery/test-email"},
	} {
		entry, _, result, _ := table.Lookup(c.method, c.path)
		if result != route.RouteFound {
			t.Fatalf("Lookup(%s, %s) result = %v, want RouteFound", c.method, c.path, result)
		}
		if !entry.Manifest.EngineNative {
			t.Errorf("Lookup(%s, %s).Manifest.EngineNative = false, want true", c.method, c.path)
		}
		if !entry.Manifest.EngineBuiltin {
			t.Errorf("Lookup(%s, %s).Manifest.EngineBuiltin = false, want true", c.method, c.path)
		}
	}
}

// TestBuildRouteTable_RegistrationRoutesDeclareRateLimits checks the
// registration routes carry their own per-IP limits (goerp#1058) and a
// neighboring builtin route still uses the engine-wide default.
func TestBuildRouteTable_RegistrationRoutesDeclareRateLimits(t *testing.T) {
	table, err := buildRouteTable(map[string]*module.LoadedModule{})
	if err != nil {
		t.Fatalf("buildRouteTable() error = %v", err)
	}

	for _, c := range []struct {
		method, path string
		want         route.RateLimitConfig
	}{
		{"POST", "/auth/register", route.RateLimitConfig{Requests: 5, WindowSeconds: 3600, Scope: "ip"}},
		{"GET", "/auth/check-slug", route.RateLimitConfig{Requests: 60, WindowSeconds: 60, Scope: "ip"}},
	} {
		entry, _, result, _ := table.Lookup(c.method, c.path)
		if result != route.RouteFound {
			t.Fatalf("Lookup(%s, %s) result = %v, want RouteFound", c.method, c.path, result)
		}
		if entry.Manifest.RateLimit == nil || *entry.Manifest.RateLimit != c.want {
			t.Errorf("Lookup(%s, %s).Manifest.RateLimit = %+v, want %+v", c.method, c.path, entry.Manifest.RateLimit, c.want)
		}
	}

	entry, _, result, _ := table.Lookup("POST", "/auth/accept-invite")
	if result != route.RouteFound {
		t.Fatalf("Lookup(POST, /auth/accept-invite) result = %v, want RouteFound", result)
	}
	if entry.Manifest.RateLimit != nil {
		t.Errorf("Lookup(POST, /auth/accept-invite).Manifest.RateLimit = %+v, want nil (engine-wide default)", entry.Manifest.RateLimit)
	}
}

// TestBuildRouteTable_IncludesNotifRoutes checks every /_notif route
// resolves as an engine-native, session-authenticated route, and that the
// static all/read-all segments resolve to their own entries, not {id}'s.
func TestBuildRouteTable_IncludesNotifRoutes(t *testing.T) {
	table, err := buildRouteTable(map[string]*module.LoadedModule{})
	if err != nil {
		t.Fatalf("buildRouteTable() error = %v", err)
	}

	const id = "0197a4f2-0000-7000-8000-000000000000"
	for _, c := range []struct{ method, path, template string }{
		{"GET", "/_notif/feed", "/_notif/feed"},
		{"GET", "/_notif/count", "/_notif/count"},
		{"POST", "/_notif/" + id + "/read", "/_notif/{id}/read"},
		{"POST", "/_notif/read-all", "/_notif/read-all"},
		{"DELETE", "/_notif/" + id, "/_notif/{id}"},
		{"DELETE", "/_notif/all", "/_notif/all"},
		{"POST", "/_notif/device-token", "/_notif/device-token"},
		{"GET", "/_notif/preferences", "/_notif/preferences"},
		{"PATCH", "/_notif/preferences", "/_notif/preferences"},
	} {
		entry, _, result, _ := table.Lookup(c.method, c.path)
		if result != route.RouteFound {
			t.Fatalf("Lookup(%s, %s) result = %v, want RouteFound", c.method, c.path, result)
		}
		if entry.PathTemplate != c.template {
			t.Errorf("Lookup(%s, %s).PathTemplate = %q, want %q", c.method, c.path, entry.PathTemplate, c.template)
		}
		if !entry.Manifest.EngineNative || entry.Manifest.EngineBuiltin || entry.Manifest.Auth != "required" {
			t.Errorf("Lookup(%s, %s).Manifest = %+v, want EngineNative, not EngineBuiltin, Auth required", c.method, c.path, entry.Manifest)
		}
	}

	// The unsubscribe link has no session: it resolves its own identity.
	for _, method := range []string{"GET", "POST"} {
		entry, _, result, _ := table.Lookup(method, "/_notif/unsubscribe")
		if result != route.RouteFound {
			t.Fatalf("Lookup(%s, /_notif/unsubscribe) result = %v, want RouteFound", method, result)
		}
		if !entry.Manifest.EngineNative || !entry.Manifest.EngineBuiltin {
			t.Errorf("Lookup(%s, /_notif/unsubscribe).Manifest = %+v, want EngineNative and EngineBuiltin", method, entry.Manifest)
		}
	}
}

// TestBuildRouteTable_IncludesActivityFollowersRoutes checks the
// /_meta/activity/followers routes resolve as engine-native,
// session-authenticated routes, and that the static followers segment
// resolves to its own entry rather than DELETE /_meta/activity/{id}'s.
func TestBuildRouteTable_IncludesActivityFollowersRoutes(t *testing.T) {
	table, err := buildRouteTable(map[string]*module.LoadedModule{})
	if err != nil {
		t.Fatalf("buildRouteTable() error = %v", err)
	}

	const id = "0197a4f2-0000-7000-8000-000000000000"
	for _, c := range []struct{ method, path, template string }{
		{"GET", "/_meta/activity/followers", "/_meta/activity/followers"},
		{"PUT", "/_meta/activity/followers", "/_meta/activity/followers"},
		{"DELETE", "/_meta/activity/followers", "/_meta/activity/followers"},
		{"DELETE", "/_meta/activity/" + id, "/_meta/activity/{id}"},
	} {
		entry, _, result, _ := table.Lookup(c.method, c.path)
		if result != route.RouteFound {
			t.Fatalf("Lookup(%s, %s) result = %v, want RouteFound", c.method, c.path, result)
		}
		if entry.PathTemplate != c.template {
			t.Errorf("Lookup(%s, %s).PathTemplate = %q, want %q", c.method, c.path, entry.PathTemplate, c.template)
		}
		if !entry.Manifest.EngineNative || entry.Manifest.EngineBuiltin || entry.Manifest.Auth != "required" {
			t.Errorf("Lookup(%s, %s).Manifest = %+v, want EngineNative, not EngineBuiltin, Auth required", c.method, c.path, entry.Manifest)
		}
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

func TestBuildRouteTable_NotificationTemplateRoutesCaptureADottedType(t *testing.T) {
	table, err := buildRouteTable(map[string]*module.LoadedModule{})
	if err != nil {
		t.Fatalf("buildRouteTable() error = %v", err)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/admin/settings/notification-templates/sales.order_confirmed/email/fr-GH"},
		{"PUT", "/admin/settings/notification-templates/sales.order_confirmed/email/fr-GH"},
		{"DELETE", "/admin/settings/notification-templates/sales.order_confirmed/email/fr-GH"},
		{"POST", "/admin/settings/notification-templates/sales.order_confirmed/email/fr-GH/preview"},
	} {
		_, params, result, _ := table.Lookup(c.method, c.path)
		if result != route.RouteFound || params["type"] != "sales.order_confirmed" || params["channel"] != "email" || params["locale"] != "fr-GH" {
			t.Errorf("Lookup(%s, %s) = %v %v, want the type, channel and locale captured", c.method, c.path, result, params)
		}
	}
}

func TestBuildPolicyRegistry_SkipsFailedModules(t *testing.T) {
	orderModel := model.ModelDeclaration{
		Name:   "order",
		Fields: []model.NamedField{{Name: "id", Def: model.UUID().Required().PrimaryKey()}},
	}
	modules := map[string]*module.LoadedModule{
		"sales": {
			Status:     module.StatusReady,
			ModelDecls: []model.ModelDeclaration{orderModel},
			Manifest:   manifest.Manifest{Policies: []manifest.Policy{{Name: "sales:order:p", AppliesTo: "sales:order:read", Condition: "true"}}},
		},
		"reports": {
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Policies: []manifest.Policy{{Name: "reports:order:p", AppliesTo: "sales:order:read", Condition: "true"}}},
		},
		"broken": {
			Status:   module.StatusFailed,
			Manifest: manifest.Manifest{Policies: []manifest.Policy{{Name: "broken:order:p", AppliesTo: "sales:order:read", Condition: "true"}}},
		},
	}

	got := buildPolicyRegistry(modules).For("sales:order:read")

	if len(got) != 2 {
		t.Fatalf("For = %d policies, want the two from loaded modules", len(got))
	}
	for _, p := range got {
		if p.Name == "broken:order:p" {
			t.Error("a failed module's policy was registered")
		}
	}
}

func TestUpdate_BuildsCronRegistryFromLoadedModules(t *testing.T) {
	cron := func(name, schedule string) manifest.CronJob {
		return manifest.CronJob{Name: name, Label: name, Schedule: schedule, Handler: name}
	}
	failed := &module.LoadedModule{Status: module.StatusFailed, Manifest: manifest.Manifest{Type: "standard", CronJobs: []manifest.CronJob{cron("never", "* * * * *")}}}

	r := &ModuleRegistry{}
	snap, err := r.Update(map[string]*module.LoadedModule{
		"billing": {Manifest: manifest.Manifest{Type: "standard", CronJobs: []manifest.CronJob{cron("invoice", "0 3 * * *"), cron("remind", "0 9 * * 1")}}},
		"crm":     {Manifest: manifest.Manifest{Type: "standard", CronJobs: []manifest.CronJob{cron("dedupe", "*/5 * * * *")}}},
		"broken":  failed,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	var got []string
	for _, e := range snap.CronRegistry().Entries() {
		got = append(got, e.Module+"."+e.Job.Name)
	}
	want := []string{"billing.invoice", "billing.remind", "crm.dedupe"}
	if !slices.Equal(got, want) {
		t.Errorf("cron entries = %v, want %v (modules in name order, failed modules skipped)", got, want)
	}
}
