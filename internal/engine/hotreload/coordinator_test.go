package hotreload

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
)

// localRedisConfig matches internal/engine/cache/client_test.go's own
// convention — the compose.dev.yml Redis instance.
func localRedisConfig() cache.Config {
	return cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1}
}

func newCacheClient(t *testing.T) *cache.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := cache.New(ctx, localRedisConfig())
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func newRegistry(t *testing.T, preloaded map[string]*module.LoadedModule) *registry.ModuleRegistry {
	t.Helper()
	reg := &registry.ModuleRegistry{}
	if preloaded == nil {
		preloaded = map[string]*module.LoadedModule{}
	}
	if _, err := reg.Update(preloaded); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
	return reg
}

// writeModuleDir writes a loose manifest.json + module.wasm module
// source under a fresh temp directory, the same layout
// moduleboot.DiscoverOne's non-.erp branch reads — enough for
// OnModuleFileChanged to produce a valid manifest.Manifest without
// needing a real compiled WASM binary, since Coordinator never compiles
// or loads the module itself (that's the leader/follower path's job).
func writeModuleDir(t *testing.T, name, version string) string {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	wasmBytes := []byte("not a real wasm binary, just fixture bytes for " + name)
	sum := sha256.Sum256(wasmBytes)
	fields := map[string]any{
		"name":         name,
		"display_name": name,
		"type":         "domain",
		"version":      version,
		"description":  "a hot reload coordinator test module",
		"abi_version":  "1",
		"engine":       ">=0.5.0 <1.0.0",
		"depends_on":   []string{},
		"capabilities": []string{},
		"schema": map[string]any{
			"owned_models": []string{"widgets.widget"},
		},
		"checksum": fmt.Sprintf("sha256:%x", sum),
	}
	manifestBytes, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "module.wasm"), wasmBytes, 0o644); err != nil {
		t.Fatalf("write module.wasm: %v", err)
	}

	return dir
}

// writeErpPackage zips a minimal valid manifest.json + module.wasm into
// a fresh *.erp file under dir — the same wire shape
// moduleboot.DiscoverOne's .erp branch (readPackageSource) reads, and
// what a real hot-reload fsnotify trigger watches for.
func writeErpPackage(t *testing.T, dir, name, version string) string {
	t.Helper()

	wasmBytes := []byte("not a real wasm binary, just fixture bytes for " + name)
	sum := sha256.Sum256(wasmBytes)
	fields := map[string]any{
		"name":         name,
		"display_name": name,
		"type":         "domain",
		"version":      version,
		"description":  "a hot reload coordinator test module",
		"abi_version":  "1",
		"engine":       ">=0.5.0 <1.0.0",
		"depends_on":   []string{},
		"capabilities": []string{},
		"schema": map[string]any{
			"owned_models": []string{"widgets.widget"},
		},
		"checksum": fmt.Sprintf("sha256:%x", sum),
	}
	manifestBytes, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeEntry := func(entryName string, data []byte) {
		w, err := zw.Create(entryName)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", entryName, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("write zip entry %s: %v", entryName, err)
		}
	}
	writeEntry("manifest.json", manifestBytes)
	writeEntry("module.wasm", wasmBytes)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	path := filepath.Join(dir, name+"-"+version+".erp")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// uniqueModuleName satisfies manifest.Manifest's name_regex (lowercase
// alphanumeric/underscore, starting with a letter) while staying unique
// across parallel test runs — t.Name() itself often isn't lowercase.
func uniqueModuleName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("widgets_%d", time.Now().UnixNano())
}

func loadedModule(t *testing.T, name, version string) *module.LoadedModule {
	t.Helper()
	return &module.LoadedModule{
		Status:   module.StatusReady,
		Manifest: manifest.Manifest{Name: name, Version: version},
	}
}

func TestOnModuleFileChanged_AlreadyCurrentVersionIsNoOp(t *testing.T) {
	c := newCacheClient(t)
	name := uniqueModuleName(t)
	reg := newRegistry(t, map[string]*module.LoadedModule{name: loadedModule(t, name, "2.0.0")})

	var leaderCalled bool
	co := New(c, reg, "instance-a", Config{}, func(ctx context.Context, moduleName string, src loader.Source, m manifest.Manifest) error {
		leaderCalled = true
		return nil
	}, nil)

	dir := writeModuleDir(t, name, "1.0.0") // older than the already-installed 2.0.0
	co.OnModuleFileChanged(context.Background(), dir)

	if leaderCalled {
		t.Error("Leader was called for a version older than what's already installed, want a no-op")
	}
}

func TestOnReloadAnnouncement_AlreadyCurrentVersionIsNoOp(t *testing.T) {
	c := newCacheClient(t)
	name := uniqueModuleName(t)
	reg := newRegistry(t, map[string]*module.LoadedModule{name: loadedModule(t, name, "2.0.0")})

	var followerCalled bool
	co := New(c, reg, "instance-a", Config{}, nil, func(ctx context.Context, moduleName, version, objectKey string) error {
		followerCalled = true
		return nil
	})

	co.OnReloadAnnouncement(context.Background(), name, "1.0.0:some-object-key")

	if followerCalled {
		t.Error("Follower was called for an announcement older than what's already installed, want a no-op")
	}
}

func TestOnReloadAnnouncement_MalformedPayloadLogsAndReturns(t *testing.T) {
	c := newCacheClient(t)
	reg := newRegistry(t, nil)

	var followerCalled bool
	co := New(c, reg, "instance-a", Config{}, nil, func(ctx context.Context, moduleName, version, objectKey string) error {
		followerCalled = true
		return nil
	})

	// No ":" separator — not parseable as "{version}:{object_key}".
	co.OnReloadAnnouncement(context.Background(), "widgets", "malformed-payload")

	if followerCalled {
		t.Error("Follower was called for a malformed announcement payload, want it logged and ignored")
	}
}

func TestOnModuleFileChanged_LosingTheLockWithNoAnnouncementTimesOut(t *testing.T) {
	c := newCacheClient(t)
	name := uniqueModuleName(t)
	reg := newRegistry(t, nil)

	// Simulate another instance already holding the lock for this exact
	// (module, version) — this Coordinator will lose the election and
	// wait, but nothing ever publishes an announcement or updates the
	// registry, so it must time out rather than hang or call Follower.
	lockKey := reloadLockKey(name, "1.0.0")
	if _, err := c.SetNXWithTTL(context.Background(), lockKey, "other-instance", time.Minute); err != nil {
		t.Fatalf("seed competing lock: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), lockKey) })

	var followerCalled bool
	co := New(c, reg, "instance-a", Config{LockTTL: 300 * time.Millisecond}, nil,
		func(ctx context.Context, moduleName, version, objectKey string) error {
			followerCalled = true
			return nil
		})

	dir := writeModuleDir(t, name, "1.0.0")

	start := time.Now()
	co.OnModuleFileChanged(context.Background(), dir)
	elapsed := time.Since(start)

	if followerCalled {
		t.Error("Follower was called even though no announcement ever arrived")
	}
	if elapsed < 300*time.Millisecond {
		t.Errorf("returned after %s, want it to have waited out the %s lock TTL", elapsed, 300*time.Millisecond)
	}
	if elapsed > 2*time.Second {
		t.Errorf("returned after %s, want it bounded close to the %s lock TTL", elapsed, 300*time.Millisecond)
	}
}

// TestOnModuleFileChanged_ConcurrentInstancesExactlyOneLeader is the core
// leader-election acceptance criterion: two Coordinators (distinct
// InstanceID, sharing one real Redis) racing OnModuleFileChanged for the
// same (module, version) must resolve to exactly one Leader call, with
// the loser's wait resolving well before its LockTTL deadline rather than
// timing out. The loser's own follower-path adoption is a separate
// concern, covered by TestCoordinator_Start_DeliversAnnouncementToFollower
// below — this test's Leader stub updates the shared registry directly
// (standing in for what a real leader path eventually would, once
// goerp#467 exists) purely so the loser's wait loop has something to
// observe, without needing a live pub/sub subscriber wired up here too.
func TestOnModuleFileChanged_ConcurrentInstancesExactlyOneLeader(t *testing.T) {
	c := newCacheClient(t)
	name := uniqueModuleName(t)
	reg := newRegistry(t, nil) // shared by both Coordinators, standing in for "this instance's own registry snapshot" being consistent per-process in reality.

	dir := writeModuleDir(t, name, "1.0.0")

	var mu sync.Mutex
	var leaderCalls, followerCalls int

	leader := func(ctx context.Context, moduleName string, src loader.Source, m manifest.Manifest) error {
		mu.Lock()
		leaderCalls++
		mu.Unlock()

		// A real leader path (compile, schema-sync every tenant) takes
		// real wall-clock time, which is what gives the loser's own
		// SETNX attempt a genuine window to race against the lock while
		// it's still held. This stub returns near-instantly otherwise,
		// so without this sleep the winner's whole sequence (including
		// its deferred lock release) can complete before the loser's
		// SETNX even reaches Redis — both then see the key unset and
		// "win", which isn't a real lock failure, just an unrealistically
		// fast stub racing itself.
		time.Sleep(200 * time.Millisecond)

		_, err := reg.Update(map[string]*module.LoadedModule{moduleName: loadedModule(t, moduleName, m.Version)})
		return err
	}
	follower := func(ctx context.Context, moduleName, version, objectKey string) error {
		mu.Lock()
		followerCalls++
		mu.Unlock()
		return nil
	}

	const lockTTL = 5 * time.Second
	coA := New(c, reg, "instance-a", Config{LockTTL: lockTTL}, leader, follower)
	coB := New(c, reg, "instance-b", Config{LockTTL: lockTTL}, leader, follower)

	var wg sync.WaitGroup
	wg.Add(2)
	start := time.Now()
	go func() {
		defer wg.Done()
		coA.OnModuleFileChanged(context.Background(), dir)
	}()
	go func() {
		defer wg.Done()
		coB.OnModuleFileChanged(context.Background(), dir)
	}()
	wg.Wait()
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	if leaderCalls != 1 {
		t.Errorf("leaderCalls = %d, want exactly 1", leaderCalls)
	}
	if followerCalls != 0 {
		t.Errorf("followerCalls = %d, want 0 — this test never wires up a pub/sub subscriber, so Follower has no path to be called from", followerCalls)
	}
	if elapsed >= lockTTL {
		t.Errorf("both goroutines took %s, want well under the %s lock TTL — the loser should have resolved via the registry catching up, not timed out", elapsed, lockTTL)
	}
}

// TestCoordinator_Start_DeliversAnnouncementToFollower proves the other
// half of the leader/follower story the test above deliberately doesn't
// cover: once Start has launched the pub/sub subscriber, a real
// Publish on engine:reload:{module} reaches OnReloadAnnouncement and
// calls Follower with the parsed version/object key — the path a losing
// instance actually takes in a running engine, as opposed to this
// package's own direct-registry-mutation test shortcut above.
func TestCoordinator_Start_DeliversAnnouncementToFollower(t *testing.T) {
	c := newCacheClient(t)
	name := uniqueModuleName(t)
	reg := newRegistry(t, nil)

	followerCalled := make(chan struct{}, 1)
	follower := func(ctx context.Context, moduleName, version, objectKey string) error {
		if moduleName == name && version == "2.0.0" && objectKey == "modules/"+name+"/2.0.0.erp" {
			followerCalled <- struct{}{}
		}
		return nil
	}

	co := New(c, reg, "instance-a", Config{}, nil, follower)
	if err := co.Start(t.Context()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(co.Stop)

	// PSubscribe's own subscription confirmation is asynchronous — see
	// cache.Client's TestPSubscribe_ReceivesMatchingPublish for the same
	// reasoning.
	time.Sleep(100 * time.Millisecond)

	if err := c.Publish(context.Background(), "engine:reload:"+name, "2.0.0:modules/"+name+"/2.0.0.erp"); err != nil {
		t.Fatalf("Publish() error: %v", err)
	}

	select {
	case <-followerCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the announcement to reach Follower")
	}
}

// TestCoordinator_Start_FSNotifyTriggersLeaderOnNewErpFile is the fsnotify
// trigger's own end-to-end test: with Start watching a real directory, a
// new *.erp file appearing there must reach OnModuleFileChanged (and so
// Leader) without any direct call into the coordinator — the same path a
// real `goerp module build` output landing in GOERP_MODULE_DIR takes.
func TestCoordinator_Start_FSNotifyTriggersLeaderOnNewErpFile(t *testing.T) {
	c := newCacheClient(t)
	name := uniqueModuleName(t)
	reg := newRegistry(t, nil)
	dir := t.TempDir()

	leaderCalled := make(chan string, 1)
	leader := func(ctx context.Context, moduleName string, src loader.Source, m manifest.Manifest) error {
		leaderCalled <- moduleName
		return nil
	}

	co := New(c, reg, "instance-a", Config{ModuleDir: dir}, leader, nil)
	if err := co.Start(t.Context()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(co.Stop)

	// Write to a temp path and rename into place, rather than
	// os.WriteFile directly into the watched dir: fsnotify can otherwise
	// deliver the Create event before every byte of a directly-written
	// file has been flushed, and DiscoverOne would then read a
	// partially-written package. A rename is atomic from the watcher's
	// perspective — it only ever sees the complete file.
	tmp := writeErpPackage(t, t.TempDir(), name, "1.0.0")
	dest := filepath.Join(dir, filepath.Base(tmp))
	if err := os.Rename(tmp, dest); err != nil {
		t.Fatalf("rename into watched dir: %v", err)
	}

	select {
	case got := <-leaderCalled:
		if got != name {
			t.Errorf("Leader called with module %q, want %q", got, name)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the fsnotify trigger to reach Leader")
	}
}

// TestCoordinator_Start_CreatesMissingModuleDir guards a real bug: on a
// fresh engine with no module ever installed, GOERP_MODULE_DIR doesn't
// exist yet (moduleinstall.Installer only creates it on a module's first
// install). Start used to call fsnotify's Watcher.Add on that path
// directly, which fails outright on a missing directory — since Start's
// error return propagates to Engine.Start, enabling hot reload on such a
// fresh deployment would have kept the whole engine from starting at
// all, not just left the fsnotify trigger disabled.
func TestCoordinator_Start_CreatesMissingModuleDir(t *testing.T) {
	c := newCacheClient(t)
	reg := newRegistry(t, nil)

	missing := filepath.Join(t.TempDir(), "does-not-exist-yet")
	co := New(c, reg, "instance-a", Config{ModuleDir: missing}, nil, nil)
	if err := co.Start(t.Context()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(co.Stop)

	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Errorf("expected Start to have created %q as a directory, stat error = %v", missing, err)
	}
}

// fakeRegistryClient stubs the not-yet-implemented external module
// registry (backlog goerp#563) for pollOnce's own tests.
type fakeRegistryClient struct {
	mu       sync.Mutex
	versions map[string]string // moduleName -> latest version reported
	calls    []string          // moduleName arguments LatestVersion was called with, in order
}

func (f *fakeRegistryClient) LatestVersion(ctx context.Context, moduleName string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, moduleName)
	v, ok := f.versions[moduleName]
	return v, ok, nil
}

// TestCoordinator_Start_PollTriggerChecksEveryLoadedModule confirms the
// registry-poll trigger (4th trigger source) actually runs on its
// ticker and checks every currently-loaded module against
// RegistryClient. It deliberately does not assert Leader/Follower gets
// called — pollOnce only detects and logs a newer version today; see
// pollOnce's own doc comment for why acting on it isn't implemented
// until goerp#563 exists.
func TestCoordinator_Start_PollTriggerChecksEveryLoadedModule(t *testing.T) {
	c := newCacheClient(t)
	name := uniqueModuleName(t)
	reg := newRegistry(t, map[string]*module.LoadedModule{name: loadedModule(t, name, "1.0.0")})

	client := &fakeRegistryClient{versions: map[string]string{name: "2.0.0"}}
	co := New(c, reg, "instance-a", Config{PollInterval: 20 * time.Millisecond, RegistryClient: client}, nil, nil)
	if err := co.Start(t.Context()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(co.Stop)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		client.mu.Lock()
		called := len(client.calls) > 0
		client.mu.Unlock()
		if called {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.calls) == 0 {
		t.Fatal("RegistryClient.LatestVersion was never called — poll ticker did not fire")
	}
	if client.calls[0] != name {
		t.Errorf("LatestVersion called with %q, want %q", client.calls[0], name)
	}
}

// TestCoordinator_Start_NilRegistryClientDisablesPolling confirms
// PollInterval alone, without a RegistryClient, does not start the poll
// trigger — Config's own doc comment documents this as the "nothing to
// poll against yet" default until goerp#563 exists.
func TestCoordinator_Start_NilRegistryClientDisablesPolling(t *testing.T) {
	c := newCacheClient(t)
	reg := newRegistry(t, nil)

	co := New(c, reg, "instance-a", Config{PollInterval: 10 * time.Millisecond, RegistryClient: nil}, nil, nil)
	if err := co.Start(t.Context()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	// If the poll trigger started despite RegistryClient being nil,
	// pollOnce's first tick (10ms) would call LatestVersion on a nil
	// interface value and panic this test well before the sleep below
	// elapses — that's the real assertion here, not just "Stop returns."
	time.Sleep(100 * time.Millisecond)
	co.Stop()
}

// TestOnModuleFileChanged_LeaderReleaseDoesNotStealAReacquiredLock guards
// a real bug found in review: onChanged's deferred lock release used to
// be an unconditional Delete by key name alone. If a leader's own Leader
// call outlives its lock's TTL, the lock can expire and be legitimately
// re-acquired by a second instance for its own leader run before the
// first instance's Leader call ever returns — an unconditional Delete at
// that point would tear down the second instance's live lock, letting a
// third instance start a third concurrent leader run for the same
// (module, version). DeleteIfEqual (keyed on InstanceID) is what makes
// the first instance's release a no-op once that's happened, instead.
func TestOnModuleFileChanged_LeaderReleaseDoesNotStealAReacquiredLock(t *testing.T) {
	c := newCacheClient(t)
	name := uniqueModuleName(t)
	reg := newRegistry(t, nil)

	const version = "1.0.0"
	dir := writeModuleDir(t, name, version)
	lockKey := reloadLockKey(name, version)

	// instance-a's own Leader call outlives its 100ms lock TTL — by the
	// time it returns, the lock has already expired in Redis and this
	// test reacquires it as "instance-c" in the gap, simulating a second
	// instance's own legitimate election win.
	leaderA := func(ctx context.Context, moduleName string, src loader.Source, m manifest.Manifest) error {
		time.Sleep(250 * time.Millisecond)
		return fmt.Errorf("leaderA: simulated slow failure")
	}
	coA := New(c, reg, "instance-a", Config{LockTTL: 100 * time.Millisecond}, leaderA, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		coA.OnModuleFileChanged(context.Background(), dir)
	}()

	// Wait past instance-a's 100ms lock TTL (well before its 250ms Leader
	// call returns and runs its deferred release), then claim the now-
	// expired key as a different owner — exactly what a second instance
	// winning a fresh election on the same key would do.
	time.Sleep(150 * time.Millisecond)
	set, err := c.SetNXWithTTL(context.Background(), lockKey, "instance-c", time.Minute)
	if err != nil {
		t.Fatalf("reacquire lock as instance-c: %v", err)
	}
	if !set {
		t.Fatal("expected the lock to have expired and be claimable by instance-c — instance-a's own lock outlived its TTL")
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), lockKey) })

	<-done // instance-a's Leader call returns and its deferred release runs here

	value, found, err := c.Get(context.Background(), lockKey)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !found || value != "instance-c" {
		t.Errorf("lock value = %q (found=%v) after instance-a's release, want unchanged %q — instance-a's release must not delete instance-c's live lock", value, found, "instance-c")
	}
}
