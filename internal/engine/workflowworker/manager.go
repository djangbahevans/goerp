// Package workflowworker downloads, verifies, and manages workflow-worker processes and
// credentials. A worker startup failure aborts engine startup; StopAll stops and de-
// authenticates every managed worker.
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

// Credential authorizes one worker process for its module's activities. It is generated
// per spawn, injected into the environment and never persisted or logged.
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

// SpawnAll attempts every qualifying non-failed module and returns joined spawn errors so
// startup reports all failed workers.
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

	taskQueue := "goerp:" + mf.Name
	if err := m.temporal.WaitForPollers(ctx, taskQueue); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("confirm task queue registration: %w", err)
	}

	p := &process{cmd: cmd, credential: credential, mf: mf, done: make(chan struct{})}

	m.mu.Lock()
	m.processes[mf.Name] = p
	m.mu.Unlock()

	go m.watch(mf.Name, p)

	return nil
}

// Respawn replaces a workflow worker and rotates its credential. It confirms that the new
// worker is polling before stopping the old process, preserving task-queue coverage and
// retaining a handle for cleanup.
func (m *Manager) Respawn(ctx context.Context, mod *module.LoadedModule) error {
	if len(mod.Manifest.WorkflowTypes) == 0 {
		return nil
	}

	m.mu.Lock()
	old, hadOld := m.processes[mod.Manifest.Name]
	m.mu.Unlock()

	// Mark replacement before waiting for pollers to prevent a stale automatic respawn.
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
		// An old worker that ignores SIGTERM must not stall reload indefinitely.
		waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		select {
		case <-old.done:
			if old.mf.WorkerChecksum != mod.Manifest.WorkerChecksum {
				if rmErr := os.RemoveAll(filepath.Join(m.cacheDir, old.mf.Name, checksumDirName(old.mf.WorkerChecksum))); rmErr != nil {
					log.Warn().Err(rmErr).Str("module", mod.Manifest.Name).
						Msg("hot reload: could not clean up old workflow-worker binary cache")
				}
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

	dir := filepath.Join(m.cacheDir, mf.Name, checksumDirName(mf.WorkerChecksum))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	binPath := filepath.Join(dir, "workflow-worker")
	file, err := os.CreateTemp(dir, ".worker-*")
	if err != nil {
		return "", fmt.Errorf("create worker cache file: %w", err)
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("cache workflow-worker binary: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close worker cache file: %w", err)
	}
	if err := os.Chmod(temporary, 0o755); err != nil {
		return "", fmt.Errorf("make cached worker executable: %w", err)
	}
	// A same-checksum reload may still have the old executable mapped by a live worker.
	if err := os.Rename(temporary, binPath); err != nil {
		return "", fmt.Errorf("publish worker cache file: %w", err)
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

// Validate checks a live module-scoped credential with constant-time comparison to avoid
// timing leaks.
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
		wg.Go(func() {
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-p.done:
			case <-ctx.Done():
				log.Warn().Str("module", name).Msg("workflow-worker did not exit before shutdown deadline")
			}
		})
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
