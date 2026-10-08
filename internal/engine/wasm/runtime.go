package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
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
	syncJobDispatcher     SyncJobDispatcher
	syncProviderTimeout   time.Duration
	providerStore         ProviderStore
	replicaDB             atomic.Pointer[sql.DB]
	schemaSyncDB          atomic.Pointer[sql.DB]
	ormBulkMaxRows        int
	ormStatementTimeout   time.Duration

	// configResolver/configStore back host.config.get/set (host_config.go).
	// Interfaces, not a direct tenantconfig dependency: tenantconfig
	// imports registry, which imports this package, so a direct import
	// here would cycle.
	configResolver ConfigResolver
	configStore    ConfigStore

	// connectorInbox backs host.connector (host_connector.go).
	connectorInbox ConnectorInbox

	notifySender NotifySender
	httpFetcher  *httpFetcher

	// rowCryptKeys encrypts/decrypts an "encrypted": true config_schema
	// entry's value (host-abi-reference.md §14), reusing the engine's
	// existing row-encryption key set.
	rowCryptKeys *rowcrypt.RowKeySet
}

// ConfigResolver resolves a fully namespaced "{module}.{key}" config
// value for a tenant — satisfied by *tenantconfig.Resolver. encrypted
// reports whether value is ciphertext from an encrypted module_config row.
type ConfigResolver interface {
	Get(ctx context.Context, tenantID, key string) (value string, encrypted, found bool, err error)

	// Invalidate drops tenantID/key's cached entry. host.config.set calls
	// this synchronously after a write, since Store.Set's own NOTIFY only
	// reaches this instance asynchronously via a Listener.
	Invalidate(tenantID, key string)
}

// ConfigStore writes a module's own declared config key into its tenant's
// module_config table — satisfied by *tenantconfig.Store.
type ConfigStore interface {
	SetModuleConfig(ctx context.Context, tenantID, tenantSchema, moduleName, key string, value []byte, valueType string, encrypted bool, updatedBy string) error
}

// SetTenantConfig wires host.config.get/set's storage layer. Unset,
// host.config.get/set return abi.unavailable.
func (r *Runtime) SetTenantConfig(resolver ConfigResolver, store ConfigStore) {
	r.configResolver = resolver
	r.configStore = store
}

// SetRowCryptKeys wires host.config.get/set's encryption layer for an
// "encrypted": true config_schema entry. Unset, a get/set touching such a
// key returns abi.unavailable rather than silently skipping encryption.
func (r *Runtime) SetRowCryptKeys(keys *rowcrypt.RowKeySet) {
	r.rowCryptKeys = keys
}

// SetSyncEventDispatcher supplies cross-module inline event dispatch after registry
// creation. Without it, synchronous emissions return an error.
func (r *Runtime) SetSyncEventDispatcher(d SyncEventDispatcher) {
	r.syncEventDispatcher = d
}

// SetSyncJobDispatcher wires the resolver host.jobs.dispatch_provider_sync
// uses to invoke another module's handle_job export in-process — set after
// New returns for the same reason as SetSyncEventDispatcher. Nil until
// then, in which case dispatch_provider_sync returns abi.unavailable.
func (r *Runtime) SetSyncJobDispatcher(d SyncJobDispatcher) {
	r.syncJobDispatcher = d
}

// SetProviderStore wires the tenant provider lookups host.jobs'
// provider-category functions route through. Unset, they return
// abi.unavailable.
func (r *Runtime) SetProviderStore(s ProviderStore) {
	r.providerStore = s
}

// SetReplicaDB supplies the read-replica pool. Replica reads return db.replica_unavailable
// while it is unset.
func (r *Runtime) SetReplicaDB(db *sql.DB) {
	r.replicaDB.Store(db)
}

// SetSchemaSyncDB supplies the BYPASSRLS pool for migration SQL and DDL.
func (r *Runtime) SetSchemaSyncDB(db *sql.DB) {
	r.schemaSyncDB.Store(db)
}

// SetDataMigrationContext grants unfiltered SQL access to a dispatched migration.
func (r *Runtime) SetDataMigrationContext(mc *ModuleContext) {
	mc.IsDataMigrationJob = true
	mc.dataMigrationDB = r.schemaSyncDB.Load()
}

// New registers the host ABI against the shared runtime. Postgres must be
// connected; an unavailable optional storage backend fails only storage calls.
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

	r := &Runtime{
		registry:              newInstanceRegistry(),
		httpFetcher:           newHTTPFetcher(),
		txLimiter:             NewTransactionLimiter(cfg.DBMaxConcurrentTransactions),
		syncSubscriberTimeout: cfg.SyncSubscriberTimeout,
		syncProviderTimeout:   cfg.SyncProviderTimeout,
		ormBulkMaxRows:        cfg.ORMBulkMaxRows,
		ormStatementTimeout:   cfg.ORMStatementTimeout,
	}

	if err := abi.RegisterAll(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host abi: %w", err)
	}

	if err := registerHostDB(ctx, rt, r, db); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.db: %w", err)
	}

	// InsertTx needs a sql.Tx client; the engine worker uses pgx.Tx.
	// Both clients share River's job table without starting a second worker.
	eventInsertClient, err := river.NewClient(riverdatabasesql.New(db), &river.Config{Schema: jobqueue.Schema})
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

	if err := registerHostJobs(ctx, rt, r, eventInsertClient); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.jobs: %w", err)
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

	if err := registerHostConfig(ctx, rt, r); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.config: %w", err)
	}

	if err := registerHostConnector(ctx, rt, r); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.connector: %w", err)
	}

	if err := registerHostCrypto(ctx, rt, r); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.crypto: %w", err)
	}

	if err := registerHostNotify(ctx, rt, r); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.notify: %w", err)
	}

	if err := registerHostCache(ctx, rt, r, cacheClient); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.cache: %w", err)
	}

	if err := registerHostHTTP(ctx, rt, r); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("register host.http: %w", err)
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
