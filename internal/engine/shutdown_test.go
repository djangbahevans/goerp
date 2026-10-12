package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type shutdownJobArgs struct{}

func (shutdownJobArgs) Kind() string { return "shutdown_test" }

type shutdownJobWorker struct {
	river.WorkerDefaults[shutdownJobArgs]
	started chan struct{}
	release chan struct{}
	result  chan error
	engine  *Engine
}

func (w *shutdownJobWorker) Work(ctx context.Context, _ *river.Job[shutdownJobArgs]) error {
	close(w.started)
	select {
	case <-w.release:
	case <-ctx.Done():
		w.result <- ctx.Err()
		return ctx.Err()
	}
	err := shutdownInfrastructureAvailable(ctx, w.engine)
	w.result <- err
	return err
}

type shutdownExporter struct {
	onShutdown func(context.Context) error
}

func (*shutdownExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (s *shutdownExporter) Shutdown(ctx context.Context) error                       { return s.onShutdown(ctx) }

func shutdownInfrastructureAvailable(ctx context.Context, e *Engine) error {
	return errors.Join(e.primaryDB.PingContext(ctx), e.replicaDB.PingContext(ctx),
		e.syncPool.Raw().PingContext(ctx), e.cacheClient.Ping(ctx))
}

func shutdownTestAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func newShutdownTestEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := baseTestConfig(t)
	cfg.ModuleDev = true
	cfg.ModuleDir = t.TempDir()
	cfg.DBReplicaDSN = cfg.DBPrimaryDSN
	cfg.AdminMaxConcurrent = 20
	cfg.ListenAddr = shutdownTestAddress(t)
	cfg.AdminAddr = shutdownTestAddress(t)
	cfg.ShutdownTimeout = 3 * time.Second
	cfg.HotReloadEnabled = true

	e, err := New(cfg)
	requireEngineConstruction(t, err)
	closeTestEnginePools(t, e)
	return e
}

func waitShutdownCondition(t *testing.T, condition func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatal("shutdown condition did not become true")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func waitShutdownResult[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown operation did not finish")
		var zero T
		return zero
	}
}

func TestShutdownDrainsInDependencyOrder(t *testing.T) {
	e := newShutdownTestEngine(t)
	e.cfg.ShutdownDrainDelay = 250 * time.Millisecond
	shutdown := sync.OnceFunc(func() { _ = e.Shutdown(context.Background()) })
	t.Cleanup(shutdown)

	worker := &shutdownJobWorker{
		started: make(chan struct{}),
		release: make(chan struct{}),
		result:  make(chan error, 1),
		engine:  e,
	}
	releaseJob := sync.OnceFunc(func() { close(worker.release) })
	t.Cleanup(releaseJob)
	workers := river.NewWorkers()
	river.AddWorker(workers, worker)
	queue, err := river.NewClient(riverpgxv5.New(e.jobQueuePool), &river.Config{
		Schema:  "system",
		Workers: workers,
		Queues: map[string]river.QueueConfig{
			jobqueue.QueueDefault: {MaxWorkers: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.jobQueue = queue

	compiled, err := e.wasmRuntime.CompileModule(t.Context(), handleRequestEchoModule)
	if err != nil {
		t.Fatal(err)
	}
	pool := wasm.NewInstancePool("shutdown-old", compiled, e.wasmRuntime, wasm.PoolConfig{
		WarmSize:      1,
		MaxSize:       2,
		BorrowTimeout: 20 * time.Millisecond,
	})
	replacement := wasm.NewInstancePool("shutdown-new", compiled, e.wasmRuntime, wasm.PoolConfig{
		WarmSize: 1,
		MaxSize:  1,
	})
	t.Cleanup(func() {
		pool.DrainAndClose(context.Background(), time.Second)
		replacement.DrainAndClose(context.Background(), time.Second)
	})
	if _, err := e.moduleRegistry.Update(map[string]*module.LoadedModule{
		"widgets": {
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Name: "widgets", Type: "standard"},
			Pool:     pool,
		},
	}); err != nil {
		t.Fatal(err)
	}
	inst, err := pool.Borrow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	returnInstance := sync.OnceFunc(func() { pool.Return(inst) })
	t.Cleanup(returnInstance)

	publicStarted := make(chan struct{})
	adminStarted := make(chan struct{})
	publicRelease := make(chan struct{})
	adminRelease := make(chan struct{})
	releasePublic := sync.OnceFunc(func() { close(publicRelease) })
	releaseAdmin := sync.OnceFunc(func() { close(adminRelease) })
	t.Cleanup(releasePublic)
	t.Cleanup(releaseAdmin)
	slowHandler := func(started, release chan struct{}) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			if err := shutdownInfrastructureAvailable(r.Context(), e); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, "completed")
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_ready", e.server.ReadyHandler())
	mux.HandleFunc("GET /slow", slowHandler(publicStarted, publicRelease))
	e.server.SetHandler(mux)
	e.adminServer.UnauthenticatedRouter().HandleFunc("GET /slow", slowHandler(adminStarted, adminRelease))
	e.adminServer.UnauthenticatedRouter().HandleFunc("GET /probe", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	flushed := make(chan bool, 1)
	e.tracerProvider = sdktrace.NewTracerProvider(sdktrace.WithSyncer(&shutdownExporter{
		onShutdown: func(ctx context.Context) error {
			flushed <- e.primaryDB.PingContext(ctx) != nil && e.replicaDB.PingContext(ctx) != nil &&
				e.syncPool.Raw().PingContext(ctx) != nil && e.cacheClient.Ping(ctx) != nil &&
				e.jobQueuePool.Stat().TotalConns() == 0
			return nil
		},
	}))
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Insert(t.Context(), shutdownJobArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	waitShutdownResult(t, worker.started)

	request := func(addr string) <-chan error {
		result := make(chan error, 1)
		go func() {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get("http://" + addr + "/slow")
			if err == nil {
				body, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				err = readErr
				if resp.StatusCode != http.StatusOK || string(body) != "completed" {
					err = fmt.Errorf("request status %d: %s", resp.StatusCode, body)
				}
			}
			result <- err
		}()
		return result
	}
	publicResult := request(e.cfg.ListenAddr)
	adminResult := request(e.cfg.AdminAddr)
	waitShutdownResult(t, publicStarted)
	waitShutdownResult(t, adminStarted)

	e.moduleRegistry.Lock()
	unlock := sync.OnceFunc(e.moduleRegistry.Unlock)
	t.Cleanup(unlock)
	done := make(chan struct{})
	go func() {
		shutdown()
		close(done)
	}()
	waitShutdownCondition(t, func() bool { return !e.readiness.Load() })
	probe := func(addr, path string) int {
		client := &http.Client{Timeout: 100 * time.Millisecond}
		resp, err := client.Get("http://" + addr + path)
		if err != nil {
			return 0
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if status := probe(e.cfg.ListenAddr, "/_ready"); status != http.StatusServiceUnavailable {
		t.Fatalf("readiness during drain delay = %d, want 503", status)
	}
	if status := probe(e.cfg.AdminAddr, "/probe"); status != http.StatusNoContent {
		t.Fatalf("admin stopped accepting during drain delay: %d", status)
	}
	waitShutdownCondition(t, func() bool { return probe(e.cfg.ListenAddr, "/_ready") == 0 })
	assertPoolStillAvailable := func() {
		t.Helper()
		borrowed, err := pool.Borrow(t.Context())
		if err != nil {
			t.Fatalf("pool drained before requests and jobs completed: %v", err)
		}
		pool.Return(borrowed)
	}
	assertPoolStillAvailable()
	releasePublic()
	if err := waitShutdownResult(t, publicResult); err != nil {
		t.Fatalf("in-flight public request: %v", err)
	}
	waitShutdownCondition(t, func() bool { return probe(e.cfg.AdminAddr, "/probe") == 0 })
	assertPoolStillAvailable()
	releaseAdmin()
	if err := waitShutdownResult(t, adminResult); err != nil {
		t.Fatalf("in-flight admin request: %v", err)
	}
	assertPoolStillAvailable()
	releaseJob()
	if err := waitShutdownResult(t, worker.result); err != nil {
		t.Fatalf("in-flight River job: %v", err)
	}
	waitShutdownCondition(t, func() bool {
		borrowed, err := pool.Borrow(t.Context())
		if err == nil {
			pool.Return(borrowed)
		}
		return errors.Is(err, wasm.ErrPoolDraining)
	})
	if err := shutdownInfrastructureAvailable(t.Context(), e); err != nil {
		t.Fatalf("infrastructure closed before pool and registry drains: %v", err)
	}
	returnInstance()

	if _, err := e.moduleRegistry.UpdateWithLocked(func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		next := maps.Clone(current)
		next["widgets"] = &module.LoadedModule{
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Name: "widgets", Type: "standard"},
			Pool:     replacement,
		}
		return next, nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("shutdown did not wait for the registry writer")
	default:
	}
	unlock()
	waitShutdownResult(t, done)
	if _, err := replacement.Borrow(t.Context()); !errors.Is(err, wasm.ErrPoolDraining) {
		t.Fatalf("replacement pool was not drained: %v", err)
	}
	if !waitShutdownResult(t, flushed) {
		t.Fatal("OpenTelemetry flushed before infrastructure closed")
	}
}

func TestShutdownRegistryTimeoutReleasesWaiterLock(t *testing.T) {
	e := newShutdownTestEngine(t)
	e.cfg.ShutdownTimeout = 50 * time.Millisecond
	e.moduleRegistry.Lock()
	unlock := sync.OnceFunc(e.moduleRegistry.Unlock)
	t.Cleanup(unlock)

	done := make(chan struct{})
	go func() {
		_ = e.Shutdown(t.Context())
		close(done)
	}()
	waitShutdownResult(t, done)
	if err := e.primaryDB.PingContext(t.Context()); err == nil {
		t.Fatal("database remained open after registry wait timed out")
	}
	unlock()

	acquired := make(chan struct{})
	go func() {
		e.moduleRegistry.Lock()
		close(acquired)
		e.moduleRegistry.Unlock()
	}()
	waitShutdownResult(t, acquired)
}

func TestShutdownStepTimeoutDoesNotConsumeNextBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := &Engine{cfg: &config.Config{ShutdownTimeout: time.Second}}
		release := make(chan struct{})
		defer close(release)
		e.shutdownStep(t.Context(), "blocked stop", func(context.Context) error {
			<-release
			return nil
		})

		budget := make(chan time.Duration, 1)
		e.shutdownStep(t.Context(), "next stop", func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				return errors.New("missing per-step deadline")
			}
			budget <- time.Until(deadline)
			return nil
		})
		if remaining := waitShutdownResult(t, budget); remaining != e.cfg.ShutdownTimeout {
			t.Fatalf("next step had %v remaining, want a fresh second", remaining)
		}
	})
}
