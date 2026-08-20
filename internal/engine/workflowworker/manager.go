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
	"syscall"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/temporal"
	"github.com/rs/zerolog/log"
)

const (
	pollerConfirmTimeout  = 30 * time.Second
	pollerConfirmInterval = 200 * time.Millisecond
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
	if err := m.confirmRegistered(ctx, taskQueue); err != nil {
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

	dir := filepath.Join(m.cacheDir, mf.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	binPath := filepath.Join(dir, "workflow-worker")
	if err := os.WriteFile(binPath, data, 0o755); err != nil {
		return "", fmt.Errorf("cache workflow-worker binary: %w", err)
	}

	return binPath, nil
}

func (m *Manager) confirmRegistered(ctx context.Context, taskQueue string) error {
	deadline := time.Now().Add(pollerConfirmTimeout)
	ticker := time.NewTicker(pollerConfirmInterval)
	defer ticker.Stop()

	for {
		has, err := m.temporal.HasPollers(ctx, taskQueue)
		if err != nil {
			return err
		}
		if has {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no poller registered on %q after %s", taskQueue, pollerConfirmTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// watch waits for p's process to exit and respawns it, unless the exit was
// caused by StopAll (m.stopping).
func (m *Manager) watch(name string, p *process) {
	_ = p.cmd.Wait()
	close(p.done)

	m.mu.Lock()
	stopping := m.stopping
	m.mu.Unlock()
	if stopping {
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
