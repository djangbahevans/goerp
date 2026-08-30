// Package workflowworker fetches, verifies, and spawns each workflow-capable
// module's workflow-worker child process (engine-internals.md §2 Stage 6
// step 30), and tracks every live process and its credential so StopAll can
// stop and de-authenticate all of them together at shutdown.
//
// Unlike Stage 3/4's per-module failure isolation, a workflow-worker that
// fails to download, verify, spawn, or register is fail-hard for the
// whole engine: engine-internals.md §2's startup-sequence section opens
// with a blanket rule for every step in Stages 1-6 ("If any step fails,
// the process exits with a non-zero code") that only Stage 1's
// individually-annotated steps and Stage 3/4's own dedicated carve-out
// paragraphs narrow — Stage 6 step 30 gets no such carve-out, so the
// default applies. SpawnAll returns an error accordingly; it does not
// mark a module module.StatusFailed the way Stage 3/4 failures do, since
// that status specifically denotes "this degrades gracefully," which a
// fail-hard startup error is not.
package workflowworker

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/temporal"
	"github.com/rs/zerolog/log"
)

// Credential authorizes exactly one workflow-worker process to call the
// activity-dispatch endpoint (goerp#255) for its own module's activities
// only (engine-internals.md §11 "Workflow-worker authentication"). It is
// generated fresh per spawn, injected into the process's environment, and
// never persisted or logged — 32 crypto/rand bytes, matching
// internal/engine/authtoken's newRefreshToken convention, without a paired
// hash since nothing needs to compare it against a stored value other than
// this in-memory map.
type Credential struct {
	Token      string
	ModuleName string
}

type process struct {
	cmd        *exec.Cmd
	credential Credential
	mf         manifest.Manifest // retained to respawn on unexpected exit
	// done is closed by watch, the sole cmd.Wait caller, once the process
	// exits — StopAll waits on it instead of calling cmd.Wait itself,
	// since concurrent Wait calls on the same *exec.Cmd are unsafe (the
	// stdlib rejects a second call outright, and the Go race detector
	// flags even the first call as racing against watch's).
	done chan struct{}
	// replaced is set by Respawn just before it signals this process to
	// stop, so watch's own exit handler can tell "Respawn deliberately
	// replaced this" apart from "this process crashed on its own" — the
	// same distinction m.stopping gives watch for a full StopAll, narrowed
	// to one process. Without it, Respawn's SIGTERM to the old process
	// would itself look like an unexpected exit and race watch's own
	// auto-respawn (using the old, just-replaced manifest) against the new
	// process Respawn already started.
	replaced atomic.Bool
}

// Manager tracks every spawned workflow-worker process and its credential —
// one Manager, shared by module load (which starts processes) and graceful
// shutdown (which stops all of them at once via StopAll).
type Manager struct {
	mu        sync.Mutex
	processes map[string]*process // module name -> process
	stopping  bool

	storage  storage.Backend
	temporal *temporal.Client
	cacheDir string
}

func NewManager(storageBackend storage.Backend, temporalClient *temporal.Client, cacheDir string) *Manager {
	return &Manager{
		processes: make(map[string]*process),
		storage:   storageBackend,
		temporal:  temporalClient,
		cacheDir:  cacheDir,
	}
}

// SpawnAll spawns a workflow-worker for every module in modules that
// declares at least one workflow type (skipping a module already
// module.StatusFailed from an earlier stage, and any module with no
// workflow types). Attempts every qualifying module rather than stopping
// at the first failure, so a caller that's about to abort startup anyway
// gets the full picture of what's broken in one error rather than one
// module at a time across repeated restarts; returns a joined error
// naming every module that failed, or nil if all of them (or none)
// succeeded — see the package doc for why this is fail-hard rather than
// per-module isolation.
func (m *Manager) SpawnAll(ctx context.Context, modules map[string]*module.LoadedModule) error {
	var errs []error
	for _, mod := range modules {
		if mod.Status == module.StatusFailed || len(mod.Manifest.WorkflowTypes) == 0 {
			continue
		}
		if err := m.spawn(ctx, mod); err != nil {
			log.Error().Err(err).Str("module", mod.Manifest.Name).Msg("failed to spawn workflow-worker")
			errs = append(errs, fmt.Errorf("module %q: %w", mod.Manifest.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) spawn(ctx context.Context, mod *module.LoadedModule) error {
	mf := mod.Manifest

	if m.storage == nil {
		return fmt.Errorf("object storage unavailable")
	}
	if m.temporal == nil {
		return fmt.Errorf("temporal client unavailable")
	}

	binPath, err := m.fetchAndVerify(ctx, mf)
	if err != nil {
		return err
	}

	credential := Credential{ModuleName: mf.Name}
	if credential.Token, err = newToken(); err != nil {
		return fmt.Errorf("generate credential: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"GOERP_WORKFLOW_WORKER_TOKEN="+credential.Token,
		"GOERP_WORKFLOW_WORKER_MODULE="+mf.Name,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	// taskQueue follows workflow-guide.md §2's stated convention
	// ("polls the module's task queue (`goerp:{module_name}`)") — one
	// queue per module, not per declared workflow type.
	taskQueue := "goerp:" + mf.Name
	if err := m.temporal.WaitForPollers(ctx, taskQueue); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait() // reap it so it doesn't linger as a zombie
		return fmt.Errorf("confirm task queue registration: %w", err)
	}

	p := &process{cmd: cmd, credential: credential, mf: mf, done: make(chan struct{})}

	m.mu.Lock()
	m.processes[mf.Name] = p
	m.mu.Unlock()

	go m.watch(mf.Name, p)

	return nil
}

// Respawn replaces mod's currently-running workflow-worker process (if
// any) with a freshly spawned one running mod's own binary, under a newly
// minted Credential — the "at module load (and hot reload), the engine
// downloads workflow-worker... verifies it... and execs it" respawn
// workflow-guide.md §3 documents, and the credential rotation
// engine-internals.md §11 describes ("hot reload: a new version gets a new
// credential, not a renewed old one"). A no-op if mod declares no
// workflow_types.
//
// Deliberately not SpawnAll: SpawnAll's own spawn unconditionally
// overwrites m.processes[name], so calling it again for an
// already-running module would leak the old process — it would keep
// running, holding its now-stale credential live, with nothing left in
// Manager tracking it to ever stop it. Respawn starts and confirms the new
// process first (spawn's own WaitForPollers call), and only then stops the
// old one — the same "old resource replaced only after the new one is
// healthy" ordering the hot-reload pool swap itself uses — so a module
// with a workflow-worker never has a window with zero live pollers for its
// task queue.
func (m *Manager) Respawn(ctx context.Context, mod *module.LoadedModule) error {
	if len(mod.Manifest.WorkflowTypes) == 0 {
		return nil
	}

	m.mu.Lock()
	old, hadOld := m.processes[mod.Manifest.Name]
	m.mu.Unlock()

	// Set before spawn, not after: spawn's own WaitForPollers call can take
	// several seconds, and if old crashes on its own during that window
	// with replaced still false, watch's exit handler would treat it as an
	// unexpected exit and auto-respawn it from its own stale manifest —
	// racing the map entry this call is about to write. Reset back to
	// false if spawn fails below, so watch's own crash-recovery still
	// covers old when this attempt to replace it didn't pan out.
	if hadOld {
		old.replaced.Store(true)
	}

	if err := m.spawn(ctx, mod); err != nil {
		if hadOld {
			old.replaced.Store(false)
		}
		return err
	}

	if hadOld {
		_ = old.cmd.Process.Signal(syscall.SIGTERM)
		// Bounded independently of ctx: Respawn runs synchronously from
		// hotreload's fsnotify trigger loop, whose own context lives for
		// the engine's whole lifetime — an old process that ignores
		// SIGTERM must not be able to wedge that loop until shutdown.
		waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		select {
		case <-old.done:
			// Best-effort: fetchAndVerify's cache directory is keyed by
			// checksum (see its own doc comment on why), so every reload of
			// a workflow-worker-bearing module leaves its previous version's
			// binary behind under a new directory unless removed here —
			// otherwise cacheDir grows by one full binary per version,
			// forever, over a long-lived deployment's repeated reloads. Only
			// removed once old is confirmed to have actually exited, never
			// on the timeout branch below, in case it's still running.
			if rmErr := os.RemoveAll(filepath.Join(m.cacheDir, old.mf.Name, checksumDirName(old.mf.WorkerChecksum))); rmErr != nil {
				log.Warn().Err(rmErr).Str("module", mod.Manifest.Name).
					Msg("hot reload: could not clean up old workflow-worker binary cache")
			}
		case <-waitCtx.Done():
			log.Warn().Str("module", mod.Manifest.Name).Msg("old workflow-worker did not exit within the wait deadline")
		}
	}

	return nil
}

func (m *Manager) fetchAndVerify(ctx context.Context, mf manifest.Manifest) (string, error) {
	rc, _, err := m.storage.Download(ctx, mf.WorkerChecksum)
	if err != nil {
		return "", fmt.Errorf("download workflow-worker binary: %w", err)
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("read workflow-worker binary: %w", err)
	}

	if err := verifyChecksum(mf.WorkerChecksum, data); err != nil {
		return "", err
	}

	// Keyed by checksum, not just module name: Respawn (goerp#467) starts
	// the new process before stopping the old one, so their two
	// fetchAndVerify calls can overlap in time for the same module — a
	// single per-module path would have this WriteFile collide with the
	// still-running old binary's own executable file (ETXTBSY on Linux).
	// Different versions almost always have different checksums, so this
	// also means a rollback to a previously-cached version never needs to
	// redownload it.
	dir := filepath.Join(m.cacheDir, mf.Name, checksumDirName(mf.WorkerChecksum))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	binPath := filepath.Join(dir, "workflow-worker")
	if err := os.WriteFile(binPath, data, 0o755); err != nil {
		return "", fmt.Errorf("cache workflow-worker binary: %w", err)
	}

	return binPath, nil
}

// watch waits for p's process to exit and respawns it, unless the exit was
// caused by StopAll (m.stopping) or by Respawn deliberately replacing p
// (p.replaced) — either way, something else is already responsible for
// what happens next to this module, so treating the exit as "unexpected"
// here would auto-respawn p's own (possibly already-superseded) binary
// racing against that other caller.
func (m *Manager) watch(name string, p *process) {
	_ = p.cmd.Wait()
	close(p.done)

	m.mu.Lock()
	stopping := m.stopping
	m.mu.Unlock()
	if stopping || p.replaced.Load() {
		return
	}

	log.Warn().Str("module", name).Msg("workflow-worker exited unexpectedly, respawning")

	mod := &module.LoadedModule{Manifest: p.mf}
	if err := m.spawn(context.Background(), mod); err != nil {
		log.Error().Err(err).Str("module", name).Msg("failed to respawn workflow-worker")
	}
}

// Validate reports whether token is currently live and authorized for
// moduleName — checked by the activity-dispatch endpoint's auth check
// (goerp#256) before it will dispatch a request. hmac.Equal, not ==,
// matching adminAuthMiddleware's own token comparison in adminapi —
// a credential compare is exactly the kind of thing a timing attack
// targets.
func (m *Manager) Validate(token, moduleName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.processes[moduleName]
	return ok && hmac.Equal([]byte(p.credential.Token), []byte(token))
}

// StopAll sends SIGTERM to every tracked process, waits up to ctx's
// deadline for each to exit, and revokes every credential regardless of
// whether its process exited cleanly in time (engine-internals.md §11).
func (m *Manager) StopAll(ctx context.Context) {
	m.mu.Lock()
	m.stopping = true
	// Snapshot rather than alias m.processes: a watch goroutine mid-respawn
	// (crash raced against this shutdown) could still write to the live
	// map after we release the lock, and ranging an unlocked map while
	// another goroutine writes it under lock is a fatal concurrent
	// map-iteration-and-write, not just a race.
	processes := maps.Clone(m.processes)
	m.mu.Unlock()

	var wg sync.WaitGroup
	for name, p := range processes {
		wg.Add(1)
		go func(name string, p *process) {
			defer wg.Done()
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-p.done:
			case <-ctx.Done():
				log.Warn().Str("module", name).Msg("workflow-worker did not exit before shutdown deadline")
			}
		}(name, p)
	}
	wg.Wait()

	m.mu.Lock()
	m.processes = make(map[string]*process) // revokes every credential
	m.mu.Unlock()
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
