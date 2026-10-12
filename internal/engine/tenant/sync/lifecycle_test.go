package tenantsync

import "testing"

func TestSyncModuleTransitions(t *testing.T) {
	env := newTestEnv(t)
	tenant := env.activeTenant(t, uniqueSlug(t))
	mod := loadedModule(t, "hooks_"+tenant.Slug)
	mod.Manifest.Version = "1.0.0"

	transition, err := SyncOneTransition(t.Context(), env.pool, env.diffEngine, tenant, mod, nil)
	if err != nil {
		t.Fatal(err)
	}
	if transition.Tenant.ID != tenant.ID || transition.OldVersion != "" || transition.Version != "1.0.0" {
		t.Fatalf("install = %+v", transition)
	}

	transition, err = SyncOneTransition(t.Context(), env.pool, env.diffEngine, tenant, mod, nil)
	if err != nil {
		t.Fatal(err)
	}
	if transition.Version != "" {
		t.Fatalf("same version = %+v", transition)
	}

	mod.Manifest.Version = "2.0.0"
	transition, err = SyncOneTransition(t.Context(), env.pool, env.diffEngine, tenant, mod, nil)
	if err != nil {
		t.Fatal(err)
	}
	if transition.OldVersion != "1.0.0" || transition.Version != "2.0.0" {
		t.Fatalf("upgrade = %+v", transition)
	}

	transition, err = SyncOneUnversionedTransition(t.Context(), env.pool, env.diffEngine, tenant, mod)
	if err != nil {
		t.Fatal(err)
	}
	if transition.Version != "" {
		t.Fatalf("same version dev resync = %+v", transition)
	}
}
