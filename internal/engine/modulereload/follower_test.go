package modulereload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/workflowworker"
)

// newFollower builds a Follower wired against env and backend (the same
// storage.Backend value a test's own Leader already published to — a real
// follower and the leader that published to it always share the one
// object-storage backend the whole cluster points at), with a fresh,
// empty registry unless preloaded overrides it. A distinct
// *registry.ModuleRegistry from any Leader in the same test simulates a
// separate instance: the only thing a follower and the leader that
// triggered it share in real deployment is object storage, the
// announcement payload, and (through TenantStore/RoleStore) the same
// Postgres — never an in-memory registry.
func newFollower(t *testing.T, env *testEnv, backend storage.Backend, preloaded map[string]*module.LoadedModule) (*Follower, *registry.ModuleRegistry) {
	t.Helper()

	reg := &registry.ModuleRegistry{}
	if preloaded != nil {
		_, _ = reg.Update(preloaded)
	} else {
		_, _ = reg.Update(map[string]*module.LoadedModule{})
	}

	t.Cleanup(func() {
		snap := reg.Snapshot()
		if snap == nil {
			return
		}
		for _, m := range snap.Modules() {
			m.Pool.DrainAndClose(context.Background(), 5*time.Second)
			_ = m.CompiledModule.Close(context.Background())
		}
	})

	return &Follower{
		Runtime:     env.rt,
		PoolCfg:     wasm.PoolConfig{MaxSize: 1, WarmSize: 0, BorrowTimeout: time.Second},
		Registry:    reg,
		RolePerms:   permcache.NewRolePermissionMap(),
		TenantStore: env.tenantStore,
		RoleStore:   env.roleStore,
		Storage:     backend,
		Workers:     workflowworker.NewManager(nil, nil, ""),
	}, reg
}

func TestFollower_Run_AdoptsLeaderPublishedModule(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	name := "widgets_" + slug
	src, mf := buildSource(t, name, "1.0.0", compileFixture(t, ""), nil)

	l, leaderReg := newLeader(t, env, nil)
	if err := l.Run(context.Background(), name, src, mf); err != nil {
		t.Fatalf("leader Run() error: %v", err)
	}
	leaderMod := leaderReg.Snapshot().Modules()[name]

	f, followerReg := newFollower(t, env, l.Storage, nil)
	if err := f.Run(context.Background(), name, mf.Version, mf.Checksum); err != nil {
		t.Fatalf("follower Run() error: %v", err)
	}

	followerMod, ok := followerReg.Snapshot().Modules()[name]
	if !ok {
		t.Fatalf("module %q not present in follower's registry after Run", name)
	}
	if followerMod.Status != module.StatusReady {
		t.Errorf("Status = %v, want StatusReady", followerMod.Status)
	}
	if followerMod.Manifest.Version != "1.0.0" {
		t.Errorf("Manifest.Version = %q, want 1.0.0", followerMod.Manifest.Version)
	}
	if followerMod == leaderMod {
		t.Error("follower's module must be its own instance, not the leader's")
	}
	// The table already exists because the leader synced it — the
	// follower path itself must never run schema sync (it has no
	// SyncPool/DiffEngine to run it with at all).
	if !tableExists(t, env.conn, "tenant_"+slug, "widgets_widget") {
		t.Error("expected the widget table the leader synced to still exist")
	}
}

// TestFollower_Run_NilStorageFailsCleanly mirrors
// TestLeader_Run_NilStorageFailsCleanly — Storage is the same possibly-nil
// storage.Backend Engine.New leaves nil after a warn-only object storage
// connect failure (engine-internals.md §2).
func TestFollower_Run_NilStorageFailsCleanly(t *testing.T) {
	env := newTestEnv(t)

	f, _ := newFollower(t, env, nil, nil)

	err := f.Run(context.Background(), "widgets_anything", "1.0.0", "sha256:doesnotmatter")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if err.Error() != "object storage unavailable" {
		t.Errorf("error = %q, want %q", err.Error(), "object storage unavailable")
	}
}

// TestFollower_Run_ChecksumMismatchAbortsBeforePublish covers this
// ticket's own acceptance criterion directly: a corrupted or tampered
// download must abort before compiling, surfacing loader.LoadModule's own
// checksum verification failure as Run's error, and must never publish
// the bad module into the registry.
func TestFollower_Run_ChecksumMismatchAbortsBeforePublish(t *testing.T) {
	env := newTestEnv(t)

	f, reg := newFollower(t, env, nil, nil)
	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New(local): %v", err)
	}
	f.Storage = backend

	name := "widgets_" + uniqueSlug(t)
	wasmBytes := compileFixture(t, "")
	// A checksum that doesn't match wasmBytes — simulating a corrupted or
	// tampered download.
	mf := map[string]any{
		"name":         name,
		"display_name": name,
		"type":         "domain",
		"version":      "1.0.0",
		"description":  "a hot reload follower test module",
		"abi_version":  "1",
		"engine":       ">=0.5.0 <1.0.0",
		"depends_on":   []string{},
		"capabilities": []string{"db.read", "db.write"},
		"schema":       map[string]any{"owned_models": []string{"widgets.widget"}},
		"checksum":     "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	}
	manifestBytes, err := json.Marshal(mf)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	objectKey := "corrupt-" + name
	if _, err := backend.Upload(context.Background(), objectKey, bytes.NewReader(wasmBytes), storage.UploadOptions{ContentType: "application/wasm"}); err != nil {
		t.Fatalf("upload binary: %v", err)
	}
	if _, err := backend.Upload(context.Background(), objectKey+".manifest.json", bytes.NewReader(manifestBytes), storage.UploadOptions{ContentType: "application/json"}); err != nil {
		t.Fatalf("upload manifest: %v", err)
	}

	err = f.Run(context.Background(), name, "1.0.0", objectKey)
	if err == nil {
		t.Fatal("expected an error for a checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error = %q, want it to mention \"checksum\"", err.Error())
	}

	if _, ok := reg.Snapshot().Modules()[name]; ok {
		t.Error("a checksum-mismatched module must never be published to the registry")
	}
}

// TestFollower_Run_ReservationReleasedOnFailure covers this ticket's own
// acceptance criterion: a follower path failure must release its
// Registry.Reserve so the next trigger (or the leader's own crash-recovery
// retry) can attempt this module again — not leave it wedged.
func TestFollower_Run_ReservationReleasedOnFailure(t *testing.T) {
	env := newTestEnv(t)

	f, reg := newFollower(t, env, nil, nil)
	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New(local): %v", err)
	}
	f.Storage = backend

	name := "widgets_" + uniqueSlug(t)

	// No binary was ever published under this key, so the download itself
	// fails — a different failure mode from the checksum-mismatch test
	// above, but the same reservation-release requirement applies.
	if err := f.Run(context.Background(), name, "1.0.0", "never-published"); err == nil {
		t.Fatal("expected a download error, got nil")
	}

	release, err := reg.Reserve(name)
	if err != nil {
		t.Fatalf("Reserve(%q) after a failed Run: %v — reservation was not released", name, err)
	}
	release()
}

func TestFollower_Run_UpgradeDrainsOldPoolWithoutMutatingStatus(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	name := "widgets_" + slug
	l, _ := newLeader(t, env, nil)
	f, followerReg := newFollower(t, env, l.Storage, nil)

	src1, mf1 := buildSource(t, name, "1.0.0", compileFixture(t, ""), nil)
	if err := l.Run(context.Background(), name, src1, mf1); err != nil {
		t.Fatalf("leader Run() v1 error: %v", err)
	}
	if err := f.Run(context.Background(), name, mf1.Version, mf1.Checksum); err != nil {
		t.Fatalf("follower Run() v1 error: %v", err)
	}
	oldMod := followerReg.Snapshot().Modules()[name]

	src2, mf2 := buildSource(t, name, "1.1.0", compileFixture(t, "1"), nil)
	if err := l.Run(context.Background(), name, src2, mf2); err != nil {
		t.Fatalf("leader Run() v2 error: %v", err)
	}
	if err := f.Run(context.Background(), name, mf2.Version, mf2.Checksum); err != nil {
		t.Fatalf("follower Run() v2 error: %v", err)
	}

	newMod := followerReg.Snapshot().Modules()[name]
	if newMod.Manifest.Version != "1.1.0" {
		t.Errorf("live version = %q, want 1.1.0", newMod.Manifest.Version)
	}
	if newMod == oldMod {
		t.Error("registry entry did not change identity across reload")
	}
	if oldMod.Status != module.StatusReady {
		t.Errorf("old module's Status was mutated to %v; Run must never mutate a superseded, still-referenceable LoadedModule in place", oldMod.Status)
	}

	// The old pool is drained asynchronously (Run's own doc comment) —
	// poll briefly for Borrow to start reporting ErrPoolDraining, the same
	// pattern TestLeader_Run_UpgradeSyncsNewColumnAndDrainsOldPool uses.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := oldMod.Pool.Borrow(context.Background())
		if errors.Is(err, wasm.ErrPoolDraining) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old pool was not draining within the deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(250 * time.Millisecond)
}

func TestFollower_Run_ConcurrentSameModuleFollows_OneSucceedsOneRejected(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	name := "widgets_" + slug
	l, _ := newLeader(t, env, nil)
	src, mf := buildSource(t, name, "1.0.0", compileFixture(t, ""), nil)
	if err := l.Run(context.Background(), name, src, mf); err != nil {
		t.Fatalf("leader Run() error: %v", err)
	}

	f, followerReg := newFollower(t, env, l.Storage, nil)

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); results[0] = f.Run(context.Background(), name, mf.Version, mf.Checksum) }()
	go func() { defer wg.Done(); results[1] = f.Run(context.Background(), name, mf.Version, mf.Checksum) }()
	wg.Wait()

	successes, rejections := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, errReloadInProgress):
			rejections++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 || rejections != 1 {
		t.Errorf("results = %v, want exactly one success and one errReloadInProgress rejection", results)
	}

	if _, ok := followerReg.Snapshot().Modules()[name]; !ok {
		t.Errorf("module %q not present in follower's registry after the successful attempt", name)
	}
}
