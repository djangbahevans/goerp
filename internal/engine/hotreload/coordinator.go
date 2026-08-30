// Package hotreload implements the multi-instance coordination
// engine-internals.md §10 documents: whichever instance's trigger fires
// first for a given (module, version) wins a Redis SET NX lock and runs
// the leader path; every other instance — including, structurally, the
// leader's own eventual receipt of its own announcement — runs the
// follower path instead. Coordinator is deliberately a narrow,
// independently constructible struct rather than methods on *Engine (the
// doc's own pseudocode shape) — the same pattern
// internal/engine/moduleinstall.Worker and
// internal/engine/registry.ModuleRegistry already use — so the
// leader-election acceptance criteria are testable against two
// Coordinators sharing one real Redis, without standing up a full
// Engine.
//
// The leader and follower paths themselves (compile, schema-sync,
// publish; download, verify, compile) are goerp#467 and its follower-path
// counterpart's own scope, not this package's — Coordinator only ever
// calls the LeaderFunc/FollowerFunc it's given.
package hotreload

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/moduleboot"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
	"golang.org/x/mod/semver"
)

// defaultLockTTL matches docs/engine-internals.md §10's reloadLockTTL —
// the leader-crash backstop: a leader that dies after acquiring the lock
// but before publishing leaves followers waiting exactly this long before
// they give up, and lets the lock expire so the next trigger can
// re-elect from scratch.
const defaultLockTTL = 60 * time.Second

// waitPollInterval is how often a follower re-checks CurrentVersionAtLeast
// while waiting for the leader's announcement, matching the doc's own
// waitForReloadAnnouncement.
const waitPollInterval = 200 * time.Millisecond

const reloadChannelPrefix = "engine:reload:"
const reloadChannelPattern = reloadChannelPrefix + "*"

// LeaderFunc runs the leader path for a module this instance won the
// election for: validate, compile, schema-sync every tenant, upload to
// object storage, warm+health-check its own pool, swap its own registry,
// then publish the engine:reload:{module} announcement on success. #452
// wires a stub that always fails until goerp#467 replaces it.
type LeaderFunc func(ctx context.Context, moduleName string, src loader.Source, m manifest.Manifest) error

// FollowerFunc runs the follower path: download the leader-published
// binary from object storage (verifying its checksum again), compile
// locally, warm+health-check its own pool, swap its own registry. Never
// runs schema sync — that already happened once, on the leader. #452
// wires a stub that always fails until the follower-path ticket replaces
// it.
type FollowerFunc func(ctx context.Context, moduleName, version, objectKey string) error

// RegistryClient is the not-yet-implemented external module registry
// service (backlog goerp#563 — scan/sign/publish/serve pipeline). A nil
// RegistryClient in Config disables the registry-poll trigger entirely:
// there is nothing to poll against yet.
type RegistryClient interface {
	// LatestVersion reports the newest version of moduleName the registry
	// knows about. ok is false when the registry has no record of
	// moduleName at all (not the same as an error).
	LatestVersion(ctx context.Context, moduleName string) (version string, ok bool, err error)
}

// Config configures which of the four trigger sources Coordinator.Start
// actually launches — the pub/sub subscriber (trigger 2) always runs,
// since every instance must always be able to receive an announcement
// regardless of which triggers are otherwise enabled.
type Config struct {
	// ModuleDir is the fsnotify watch root (trigger 1). Empty disables
	// the fsnotify trigger. Only top-level *.erp entries are watched —
	// the loose manifest.json+module.wasm directory layout is a
	// goerp module create dev convenience moduleboot.Discover also
	// supports, but isn't itself watched here.
	ModuleDir string
	// LockTTL overrides defaultLockTTL when positive.
	LockTTL time.Duration
	// PollInterval is the registry-poll ticker's (trigger 4) cadence. Zero
	// disables the poll trigger regardless of RegistryClient.
	PollInterval time.Duration
	// RegistryClient is nil until goerp#563 exists — see RegistryClient's
	// own doc comment.
	RegistryClient RegistryClient
}

// Coordinator implements the four trigger sources and the two
// coordination entry points (OnModuleFileChanged, OnReloadAnnouncement)
// docs/engine-internals.md §10 documents. Construct with New; call
// Start/Stop to run the fsnotify/pub-sub/poll trigger goroutines, or call
// OnModuleFileChanged/OnModuleBytesChanged/OnReloadAnnouncement directly
// (e.g. from the Admin API handler, or from a test) without Start.
type Coordinator struct {
	Cache      *cache.Client
	Registry   *registry.ModuleRegistry
	InstanceID string
	Leader     LeaderFunc
	Follower   FollowerFunc

	cfg    Config
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New builds a Coordinator. leader and follower are called only once
// this instance has won (leader) or lost (follower, once an announcement
// arrives) the election for a given (module, version) — see LeaderFunc/
// FollowerFunc's own doc comments.
func New(cacheClient *cache.Client, reg *registry.ModuleRegistry, instanceID string, cfg Config, leader LeaderFunc, follower FollowerFunc) *Coordinator {
	if cfg.LockTTL <= 0 {
		cfg.LockTTL = defaultLockTTL
	}
	return &Coordinator{
		Cache:      cacheClient,
		Registry:   reg,
		InstanceID: instanceID,
		Leader:     leader,
		Follower:   follower,
		cfg:        cfg,
	}
}

// Start launches the pub/sub subscriber (always) and, as configured, the
// fsnotify watcher and registry-poll ticker, all stopped together by
// Stop. The provided ctx bounds their lifetime beyond Stop too (e.g. a
// process-wide shutdown context).
func (c *Coordinator) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	msgs, closeSub := c.Cache.PSubscribe(ctx, reloadChannelPattern)
	c.wg.Go(func() {
		defer func() { _ = closeSub() }()
		for msg := range msgs {
			moduleName := strings.TrimPrefix(msg.Channel, reloadChannelPrefix)
			c.OnReloadAnnouncement(ctx, moduleName, msg.Payload)
		}
	})

	if c.cfg.ModuleDir != "" {
		// ModuleDir may not exist yet on a fresh engine with no modules
		// ever installed — moduleinstall.Installer.StartInstall only
		// creates it on a module's first install, and
		// moduleboot.Discover itself treats a missing directory as "not
		// an error, just empty" for the same reason. Creating it here
		// keeps Start from failing outright (and so keeping the whole
		// engine from starting) just because an operator enabled hot
		// reload before anything was ever installed.
		if err := os.MkdirAll(c.cfg.ModuleDir, 0o755); err != nil {
			cancel()
			return fmt.Errorf("create module directory %q: %w", c.cfg.ModuleDir, err)
		}

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			cancel()
			return fmt.Errorf("create fsnotify watcher: %w", err)
		}
		if err := watcher.Add(c.cfg.ModuleDir); err != nil {
			_ = watcher.Close()
			cancel()
			return fmt.Errorf("watch module directory %q: %w", c.cfg.ModuleDir, err)
		}
		c.wg.Go(func() {
			defer func() { _ = watcher.Close() }()
			c.runFSWatcher(ctx, watcher)
		})
	}

	if c.cfg.RegistryClient != nil && c.cfg.PollInterval > 0 {
		c.wg.Go(func() {
			c.runPoller(ctx)
		})
	}

	return nil
}

// Stop cancels every trigger goroutine Start launched and waits for them
// to actually exit.
func (c *Coordinator) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
}

func (c *Coordinator) runFSWatcher(ctx context.Context, watcher *fsnotify.Watcher) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			if !strings.HasSuffix(event.Name, ".erp") {
				continue
			}
			c.OnModuleFileChanged(ctx, event.Name)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Error().Err(err).Msg("hot reload: fsnotify watcher error")
		}
	}
}

func (c *Coordinator) runPoller(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.pollOnce(ctx)
		}
	}
}

// pollOnce checks every currently-loaded module against RegistryClient
// and logs when a newer version exists. It deliberately stops there: a
// RegistryClient reports only a version string, not fetchable content, so
// there is nothing yet to hand to onChanged — see RegistryClient's own
// doc comment. Once goerp#563 exists and RegistryClient grows a real
// fetch path, this is where OnModuleFileChanged (or an equivalent
// bytes-based entry point) would be called.
func (c *Coordinator) pollOnce(ctx context.Context) {
	snap := c.Registry.Snapshot()
	if snap == nil {
		return
	}
	for name, m := range snap.Modules() {
		if m.Status == module.StatusFailed {
			continue
		}
		version, ok, err := c.cfg.RegistryClient.LatestVersion(ctx, name)
		if err != nil {
			log.Warn().Err(err).Str("module", name).Msg("hot reload: registry poll failed")
			continue
		}
		if !ok || c.CurrentVersionAtLeast(name, version) {
			continue
		}
		log.Info().Str("module", name).Str("version", version).
			Msg("hot reload: registry poll found a newer version, but fetching it is not yet implemented (goerp#563)")
	}
}

// OnModuleFileChanged is the fsnotify trigger's (and, once goerp#563
// lands, the registry-poll trigger's) entry point: path is a single
// *.erp package or loose module directory, read via
// moduleboot.DiscoverOne so this shares Discover's own parsing logic
// rather than duplicating it.
func (c *Coordinator) OnModuleFileChanged(ctx context.Context, path string) {
	src, err := moduleboot.DiscoverOne(path)
	if err != nil {
		log.Error().Err(err).Str("path", path).Msg("hot reload: could not read module source")
		return
	}
	if src == nil {
		return // missing manifest.json/module.wasm — DiscoverOne already logged a warning
	}

	m, err := manifest.Load(src.ManifestBytes)
	if err != nil {
		log.Error().Err(err).Str("path", path).Msg("hot reload: manifest load failed")
		return
	}

	c.onChanged(ctx, src.Name, *src, *m)
}

// OnModuleBytesChanged is the Admin API upload trigger's entry point:
// data is a whole .erp package already in memory (the request body),
// parsed via moduleboot.ParsePackage — the same wire format
// POST /admin/modules/install already uses, rather than the bare
// binary-plus-sibling-manifest convention OnModuleFileChanged's
// fsnotify callers use. moduleName is the reload target the request URL
// named; a mismatch against the package's own declared name is logged
// but not fatal — the package's own name is what's actually installed.
func (c *Coordinator) OnModuleBytesChanged(ctx context.Context, moduleName string, data []byte) {
	src, m, err := moduleboot.ParsePackage(data)
	if err != nil {
		log.Error().Err(err).Str("module", moduleName).Msg("hot reload: package parse failed")
		return
	}
	if src.Name != moduleName {
		log.Warn().Str("path_module", moduleName).Str("package_module", src.Name).
			Msg("hot reload: uploaded package name does not match the reload target URL; using the package's own declared name")
	}

	c.onChanged(ctx, src.Name, *src, *m)
}

// onChanged is the lock/leader/wait sequence OnModuleFileChanged and
// OnModuleBytesChanged both reduce to once they've each produced a
// loader.Source + manifest.Manifest from their own different input
// shapes.
func (c *Coordinator) onChanged(ctx context.Context, moduleName string, src loader.Source, m manifest.Manifest) {
	if c.CurrentVersionAtLeast(moduleName, m.Version) {
		return // this instance is already running this version or newer
	}

	lockKey := reloadLockKey(moduleName, m.Version)
	acquired, err := c.Cache.SetNXWithTTL(ctx, lockKey, c.InstanceID, c.cfg.LockTTL)
	if err != nil {
		log.Error().Err(err).Str("module", moduleName).Msg("hot reload: lock acquisition failed")
		return
	}

	if acquired {
		defer func() {
			// DeleteIfEqual, not Delete: if Leader took longer than
			// c.cfg.LockTTL, this instance's own lock may have already
			// expired and been re-acquired by a different instance for a
			// fresh leader run of its own — an unconditional Delete would
			// tear down that instance's live lock instead of finding
			// nothing left to release.
			if _, err := c.Cache.DeleteIfEqual(context.Background(), lockKey, c.InstanceID); err != nil {
				log.Warn().Err(err).Str("module", moduleName).Msg("hot reload: could not release lock")
			}
		}()
		if err := c.Leader(ctx, moduleName, src, m); err != nil {
			log.Error().Err(err).Str("module", moduleName).Str("version", m.Version).Msg("hot reload (leader) failed")
		}
		return // Leader publishes the announcement itself on success
	}

	// Lost the election — another instance is leading this exact
	// (module, version). Wait for its announcement rather than retrying
	// the lock; only one instance should ever run schema sync for a
	// given pair.
	if !c.waitForReloadAnnouncement(ctx, moduleName, m.Version, c.cfg.LockTTL) {
		log.Warn().Str("module", moduleName).Str("version", m.Version).
			Msg("hot reload: leader announcement never arrived — it may have crashed mid-reload; the expired lock lets the next trigger retry")
	}
}

// OnReloadAnnouncement is the pub/sub subscriber's callback for
// engine:reload:{module_name} — every instance receives every
// announcement, including the leader that published it (its own
// CurrentVersionAtLeast check below is what makes that receipt a no-op
// rather than a special case).
func (c *Coordinator) OnReloadAnnouncement(ctx context.Context, moduleName, payload string) {
	version, objectKey, ok := parseReloadPayload(payload)
	if !ok {
		log.Error().Str("module", moduleName).Str("payload", payload).Msg("hot reload: malformed announcement payload")
		return
	}

	if c.CurrentVersionAtLeast(moduleName, version) {
		return // our own echo, or we already adopted this (or a newer) version some other way
	}

	if err := c.Follower(ctx, moduleName, version, objectKey); err != nil {
		log.Error().Err(err).Str("module", moduleName).Str("version", version).Msg("hot reload (follower) failed")
		// Left running the old version. The next local trigger re-enters
		// onChanged, which will almost certainly lose the lock again (the
		// module is already installed on every other instance) and retry
		// this same follower path.
	}
}

// CurrentVersionAtLeast compares version against whatever this
// instance's own registry snapshot already has installed for moduleName.
// It is the one check that stops a leader's own pub/sub receipt of its
// own announcement from re-running the follower path on itself, and
// equally stops a stale or reordered announcement from regressing an
// instance that's already moved past it.
func (c *Coordinator) CurrentVersionAtLeast(moduleName, version string) bool {
	snap := c.Registry.Snapshot()
	if snap == nil {
		return false
	}
	mod, ok := snap.Modules()[moduleName]
	if !ok {
		return false
	}
	return semver.Compare(asSemver(mod.Manifest.Version), asSemver(version)) >= 0
}

func (c *Coordinator) waitForReloadAnnouncement(ctx context.Context, moduleName, version string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if c.CurrentVersionAtLeast(moduleName, version) {
			return true
		}
		if !time.Now().Before(deadline) {
			return c.CurrentVersionAtLeast(moduleName, version)
		}
		select {
		case <-ctx.Done():
			return c.CurrentVersionAtLeast(moduleName, version)
		case <-time.After(waitPollInterval):
		}
	}
}

func reloadLockKey(moduleName, version string) string {
	return fmt.Sprintf("engine:reload:lock:%s:%s", moduleName, version)
}

// parseReloadPayload splits an announcement payload of the form
// "{version}:{object_key}" — either half missing is malformed.
func parseReloadPayload(payload string) (version, objectKey string, ok bool) {
	version, objectKey, found := strings.Cut(payload, ":")
	if !found || version == "" || objectKey == "" {
		return "", "", false
	}
	return version, objectKey, true
}

// asSemver adapts a bare manifest version ("1.2.3", manifest.Manifest's
// own validated format) to golang.org/x/mod/semver's required "v"-prefixed
// form — without it, semver.Compare treats both sides as equally invalid
// and always reports them equal, silently breaking every version
// comparison in this package.
func asSemver(version string) string {
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}
