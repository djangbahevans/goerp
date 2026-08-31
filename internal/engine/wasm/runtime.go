package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type Runtime struct {
	wazero       wazero.Runtime
	moduleConfig wazero.ModuleConfig
	modules      map[string]api.Module

	registry              instanceRegistry
	txLimiter             *TransactionLimiter
	eventInsertClient     *river.Client[*sql.Tx]
	syncEventDispatcher   SyncEventDispatcher
	syncSubscriberTimeout time.Duration
	replicaDB             atomic.Pointer[sql.DB]
}

// SetSyncEventDispatcher wires the resolver host.event.emit's inline
// synchronous dispatch (goerp#129) uses to invoke another module's
// handle_event export. Set after New returns, once
// registry.ModuleRegistry exists (engine.go constructs it after
// wasm.New, and the wasm package cannot import registry itself — see
// SyncEventDispatcher's own doc comment) — nil until then, in which case
// a "sync": true emission fails with a clear error rather than a nil
// dereference.
func (r *Runtime) SetSyncEventDispatcher(d SyncEventDispatcher) {
	r.syncEventDispatcher = d
}

// SetReplicaDB wires the read-replica pool host.db.query's opts.read_only
// routing and host.db.query_replica (goerp#459) query against — set after
// New returns, the same reason SetSyncEventDispatcher is: replica
// Postgres is a warn-only Stage 1 dependency (engine-internals.md §2), so
// engine.go doesn't necessarily have a connected pool in hand yet at the
// point it calls wasm.New (nor is the replica pool's own lifecycle tied
// to the wasm Runtime's — engine.go closes it independently). Threading
// it through New's constructor instead would force every one of this
// package's ~15 existing test call sites (none of which exercise replica
// routing) to pass an extra, almost always nil, argument. Never called
// with a nil db.DB — a genuinely absent replica leaves this unset (nil
// atomic.Pointer, its zero value), and host.db.query/query_replica's own
// nil-guard turns that into db.replica_unavailable rather than a
// nil-pointer panic.
func (r *Runtime) SetReplicaDB(db *sql.DB) {
	r.replicaDB.Store(db)
}

// New builds the shared wazero runtime and registers the host ABI against
// it. db is the primary connection pool host.db's transaction-lifecycle
// functions (host-abi-reference.md §5) open transactions on — it must
// already be connected by the time New is called, since registerHostDB
// closes over it while building the host.db module. storageBackend backs
// host.storage.upload — it is a warn-only dependency (engine.go's
// storage.New already logs-and-continues on failure), so it may be nil
// here; registerHostStorage's closures nil-guard it themselves.
// cacheClient backs Transient-model host.orm routing (goerp#344) — unlike
// storageBackend, Redis is fail-hard at Stage 1 (engine-internals.md §2),
// so cacheClient is never nil by the time New is called.
//
// replicaDB (host.db.query's opts.read_only routing and
// host.db.query_replica) is not a New parameter — see SetReplicaDB's own
// doc comment for why.
func New(cfg *config.Config, db *sql.DB, storageBackend storage.Backend, cacheClient *cache.Client) (*Runtime, error) {
	ctx := context.Background()

	cacheDir := cfg.CompilationCache
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("create compilation cache directory: %w", err)
	}

	cache, err := wazero.NewCompilationCacheWithDir(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("open compilation cache: %w", err)
	}

	runtimeCfg := wazero.NewRuntimeConfig().
		WithCompilationCache(cache).
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(cfg.PoolMaxMemoryByes / (64 * manifest.KB))
	if cfg.Environment == string(config.Development) {
		runtimeCfg = runtimeCfg.WithDebugInfoEnabled(true)
	}

	rt := wazero.NewRuntimeWithConfig(ctx, runtimeCfg)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("instantiate wasi: %w", err)
	}

	// r is constructed before any host module is registered so that
	// registerHostDB's closures (below) can already close over it —
	// InstanceForModule/RegisterInstance/UnregisterInstance are populated
	// per-request later (by invokeHandler), but the registry and limiter
	// themselves must exist now.
	r := &Runtime{
		registry:              newInstanceRegistry(),
		txLimiter:             NewTransactionLimiter(cfg.DBMaxConcurrentTransactions),
		syncSubscriberTimeout: cfg.SyncSubscriberTimeout,
	}

	if err := abi.RegisterAll(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host abi: %w", err)
	}

	if err := registerHostDB(ctx, rt, r, db); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.db: %w", err)
	}

	// eventInsertClient is a separate, never-started river.Client[*sql.Tx]
	// purely so host.event.emit_tx's InsertTx can accept the stdlib *sql.Tx
	// modCtx.Transaction returns — the engine's own job-working client
	// (jobqueue.New) is pgx-based and generic over pgx.Tx, incompatible
	// with that transaction type. A job inserted through this client is
	// fully visible to and worked by the pgx-based client regardless
	// (River's job table is driver-agnostic) — see registerHostEvent's
	// own doc comment for the full reasoning. Constructed before
	// registerHostORM (not just registerHostEvent) since host.orm's write
	// half (goerp#343) also emits orm.record.* events transactionally
	// through this same client.
	eventInsertClient, err := river.NewClient(riverdatabasesql.New(db), &river.Config{})
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("create event insert client: %w", err)
	}

	if err := registerHostORM(ctx, rt, r, db, eventInsertClient, cacheClient); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.orm: %w", err)
	}

	if err := registerHostEvent(ctx, rt, r, eventInsertClient); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.event: %w", err)
	}

	filesStore := files.NewStore(db)
	if err := registerHostStorage(ctx, rt, r, storageBackend, filesStore, storageUploadLimits{
		maxFileBytes: cfg.StorageMaxFileBytes,
		allowedTypes: cfg.StorageAllowedTypes,
		blockedTypes: cfg.StorageBlockedTypes,
	}); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.storage: %w", err)
	}

	if err := registerHostAuthz(ctx, rt, r); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.authz: %w", err)
	}

	if err := registerHostSearch(ctx, rt, r, db); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.search: %w", err)
	}

	stdout := log.With().Str("component", "wasm").Str("stream", "stdout").Logger()
	stderr := log.With().Str("component", "wasm").Str("stream", "stderr").Logger()

	moduleConfig := wazero.NewModuleConfig().
		WithStdout(stdout).
		WithStderr(stderr)

	r.wazero = rt
	r.moduleConfig = moduleConfig
	r.eventInsertClient = eventInsertClient
	return r, nil
}

func (r *Runtime) ModuleConfig() wazero.ModuleConfig {
	return r.moduleConfig
}

func (r *Runtime) Close(ctx context.Context) error {
	return r.wazero.Close(ctx)
}

func (r *Runtime) alloc(ctx context.Context, module api.Module, size uint64) (uint32, error) {
	fn := module.ExportedFunction("allocate")
	if fn == nil {
		return 0, fmt.Errorf("module missing allocate export")
	}
	results, err := fn.Call(ctx, size)
	if err != nil {
		return 0, fmt.Errorf("allocate %d bytes: %w", size, err)
	}

	ptr := uint32(results[0])
	if ptr == 0 {
		return 0, abi.ErrAllocationFailed
	}

	return ptr, nil
}

func (r *Runtime) dealloc(ctx context.Context, module api.Module, ptr, size uint64) error {
	fn := module.ExportedFunction("deallocate")
	if fn == nil {
		return fmt.Errorf("module missing deallocate export")
	}
	_, err := fn.Call(ctx, ptr, size)
	if err != nil {
		return fmt.Errorf("deallocate %d bytes at ptr=%d: %w", size, ptr, err)
	}

	return nil
}

func (r *Runtime) Call(ctx context.Context, moduleName, fnName string, payload []byte) (int32, error) {
	module, ok := r.modules[moduleName]
	if !ok {
		return 0, fmt.Errorf("could not find module %s", moduleName)
	}

	ptr, err := r.alloc(ctx, module, uint64(len(payload)))
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := r.dealloc(ctx, module, uint64(ptr), uint64(len(payload))); err != nil {
			log.Warn().Err(err).Msg("could not deallocate request buffer")
		}
	}()

	if !module.Memory().Write(uint32(ptr), payload) {
		return 0, fmt.Errorf("memory.Write out of bounds at ptr=%d len=%d", ptr, len(payload))
	}

	fn := module.ExportedFunction(fnName)
	if fn == nil {
		return 0, fmt.Errorf("could not find exported function %s", fnName)
	}
	results, err := fn.Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		return 0, fmt.Errorf("call %s: %w", fnName, err)
	}

	return int32(results[0]), nil
}

func (r *Runtime) CallAndRead(ctx context.Context, moduleName string, fnName string, payload []byte) ([]byte, error) {
	module, ok := r.modules[moduleName]
	if !ok {
		return nil, fmt.Errorf("could not find module %s", moduleName)
	}

	ptr, err := r.alloc(ctx, module, uint64(len(payload)))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := r.dealloc(ctx, module, uint64(ptr), uint64(len(payload))); err != nil {
			log.Warn().Err(err).Msg("could not deallocate request buffer")
		}
	}()

	if !module.Memory().Write(uint32(ptr), payload) {
		return nil, fmt.Errorf("memory.Write out of bounds at ptr=%d len=%d", ptr, len(payload))
	}

	fn := module.ExportedFunction(fnName)
	if fn == nil {
		return nil, fmt.Errorf("could not find exported function %s", fnName)
	}
	results, err := fn.Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", fnName, err)
	}

	raw := results[0]
	ptr = uint32(raw >> 32)
	length := uint32(raw)
	data, ok := module.Memory().Read(ptr, length)
	if !ok {
		return nil, fmt.Errorf("could not read the data at ptr=%d len=%d", ptr, length)
	}

	if err := r.dealloc(ctx, module, uint64(ptr), uint64(length)); err != nil {
		log.Warn().Err(err).Msg("could not deallocate response buffer")
	}

	return data, nil
}

// tempSeq gives every InstantiateTemp call a distinct wazero module name —
// InstantiateModule rejects a name already in use by a live instance, and
// nothing otherwise guarantees a caller can't have two temp instances of
// the same named module outstanding at once (e.g. a load racing a hot
// reload of the same module).
var tempSeq atomic.Int64

// CompileModule compiles a module's WASM binary to native code, or loads
// it from the on-disk compilation cache keyed by its own checksum (§3
// "Compilation cache" — wazero's own cache, not engine-side logic).
func (r *Runtime) CompileModule(ctx context.Context, binary []byte) (wazero.CompiledModule, error) {
	return r.wazero.CompileModule(ctx, binary)
}

// NewPool builds a module's instance pool against the shared runtime.
func (r *Runtime) NewPool(name string, compiled wazero.CompiledModule, cfg PoolConfig) *InstancePool {
	return NewInstancePool(name, compiled, r.wazero, cfg)
}

// InstantiateTemp creates a throwaway instance for one-off export calls —
// get_routes/get_model_declarations/get_data_migrations at load time
// (engine-internals.md §2 Stage 3 steps 17a-17d). Construction matches a
// pooled instance exactly (same newModuleInstance), so a temp instance
// exposes the same exports and runs the same init hook a pooled instance
// would.
func (r *Runtime) InstantiateTemp(ctx context.Context, name string, compiled wazero.CompiledModule) (*ModuleInstance, error) {
	tempName := fmt.Sprintf("%s-temp-%d", name, tempSeq.Add(1))
	return newModuleInstance(ctx, tempName, compiled, r.wazero)
}
