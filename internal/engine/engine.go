// Package engine is the composition root: Engine.New runs Stage 1
// (engine-internals.md §2) — secrets backend, primary/replica Postgres,
// Redis, Meilisearch (optional), object storage — plus bootstrapping the
// system-schema tables owned outright by the engine rather than any module
// (schema.SchemaSyncPool's module_schema_versions, tenant.Store's
// tenants/tenant_domains) — and wires the results into the HTTP server's
// injected health/ready checks. Primary Postgres, Redis, and both system-
// schema bootstraps are fail-hard (New returns an error); replica Postgres,
// Meilisearch, and object storage failures only warn and continue, per the
// explicit warn-only list in engine-internals.md §2. Stage 2+ (module
// loading, schema sync, instance pooling) is out of scope here — see #34's
// notes for why, and #13/#19 for where that lands.
package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/adminapi"
	"github.com/djangbahevans/goerp/internal/engine/auditlog"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/httpx"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/mailer"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/search"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

type Engine struct {
	cfg         *config.Config
	wasmRuntime *wasm.Runtime

	syncPool    *schema.SchemaSyncPool
	tenantStore *tenant.Store

	secretsBackend secrets.Backend
	primaryDB      *sql.DB
	replicaDB      *sql.DB
	cacheClient    *cache.Client
	searchClient   *search.Client
	storageBackend storage.Backend
	server         *httpx.Server
	adminServer    *adminapi.Server
	readiness      atomic.Bool

	instancesMu sync.Mutex
	instances   map[api.Module]*wasm.ModuleInstance
}

func New(cfg *config.Config) (*Engine, error) {
	ctx := context.Background()

	secretsBackend, err := secrets.New(cfg.SecretsBackend)
	if err != nil {
		return nil, fmt.Errorf("create secrets backend: %w", err)
	}

	adminToken, err := secretsBackend.Get(ctx, "GOERP_ADMIN_TOKEN")
	if err != nil {
		return nil, fmt.Errorf("load admin token: %w", err)
	}

	primaryPool, err := db.New(cfg.DBPrimaryDSN)
	if err != nil {
		return nil, fmt.Errorf("connect to primary database: %w", err)
	}

	var replicaPool *sql.DB
	if cfg.DBReplicaDSN != "" {
		replicaPool, err = db.New(cfg.DBReplicaDSN)
		if err != nil {
			log.Warn().Err(err).Msg("could not connect to replica db")
		}
	}

	schemaPool, err := db.New(cfg.DBSchemaSyncDSN)
	if err != nil {
		_ = primaryPool.Close()
		if replicaPool != nil {
			_ = replicaPool.Close()
		}
		return nil, fmt.Errorf("connect to schema sync database: %w", err)
	}

	syncPool := schema.NewPool(schemaPool, 30*time.Second)
	if err := syncPool.Bootstrap(ctx); err != nil {
		_ = primaryPool.Close()
		_ = schemaPool.Close()
		if replicaPool != nil {
			_ = replicaPool.Close()
		}
		return nil, fmt.Errorf("bootstrap schema sync tracking table: %w", err)
	}

	tenantStore := tenant.NewStore(primaryPool)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		_ = primaryPool.Close()
		_ = schemaPool.Close()
		if replicaPool != nil {
			_ = replicaPool.Close()
		}
		return nil, fmt.Errorf("bootstrap tenant registry: %w", err)
	}

	userStore := user.NewStore(primaryPool)
	if err := userStore.Bootstrap(ctx); err != nil {
		_ = primaryPool.Close()
		_ = schemaPool.Close()
		if replicaPool != nil {
			_ = replicaPool.Close()
		}
		return nil, fmt.Errorf("bootstrap user identity store: %w", err)
	}

	// roleStore's Bootstrap is per-tenant (roles/role_permissions/
	// user_roles live in each tenant's own schema, not system) — invoked
	// once a tenant schema actually exists, by provisioning (goerp#149),
	// not here at engine startup.
	roleStore := role.NewStore(primaryPool)
	inviteMailer := mailer.New(mailer.Config{
		Host:    cfg.SMTPHost,
		Port:    cfg.SMTPPort,
		User:    cfg.SMTPUser,
		Pass:    cfg.SMTPPass,
		From:    cfg.SMTPFrom,
		BaseURL: cfg.AppBaseURL,
	})
	// audit stays nil until goerp#16 lands.
	inviteStore := invite.NewStore(primaryPool, userStore, roleStore, nil, inviteMailer)

	auditStore := auditlog.NewStore(primaryPool)
	if err := auditStore.Bootstrap(ctx); err != nil {
		_ = primaryPool.Close()
		_ = schemaPool.Close()
		if replicaPool != nil {
			_ = replicaPool.Close()
		}
		return nil, fmt.Errorf("bootstrap admin audit log: %w", err)
	}

	adminServer, err := adminapi.NewServer(&adminapi.Config{
		ListenAddr:    cfg.AdminAddr,
		AdminToken:    adminToken,
		MaxBodyBytes:  cfg.AdminMaxBodyBytes,
		MaxConcurrent: cfg.AdminMaxConcurrent,
		AuditStore:    auditStore,
	})
	if err != nil {
		_ = primaryPool.Close()
		_ = schemaPool.Close()
		if replicaPool != nil {
			_ = replicaPool.Close()
		}
		return nil, fmt.Errorf("create admin server: %w", err)
	}

	adminapi.RegisterTenantRoutes(adminServer.Router(), adminapi.TenantDeps{
		Store:       tenantStore,
		SyncStatus:  syncPool,
		TableCounts: syncPool,
		Membership:  roleStore,
		Users:       userStore,
		Inviter:     inviteStore,
		// Provisioner, Exporter, Importer, Offboarder stay nil until
		// goerp#149/#15/#150 land — the handlers report
		// StatusNotImplemented for those routes rather than the wiring
		// needing a placeholder implementation here. inviteStore's own
		// audit seam is nil until goerp#16 lands, same nil-safe pattern.
	})

	cacheClient, err := cache.New(ctx, cache.Config{
		Addr:          cfg.RedisAddr,
		MasterName:    cfg.RedisSentinelMaster,
		SentinelAddrs: cfg.RedisSentinelAddrs,
		DB:            cfg.RedisDB,
		MaxRetries:    cfg.RedisMaxRetries,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to redis: %w", err)
	}

	var searchClient *search.Client
	if cfg.MeilisearchURL != "" {
		searchClient, err = search.New(cfg.MeilisearchURL, cfg.MeilisearchAPIKey)
		if err != nil {
			log.Warn().Err(err).Msg("could not connect to meillisearch")
		}
	}

	storageBackend, err := storage.New(cfg.StorageBackend)
	if err != nil {
		log.Warn().Err(err).Msg("could not connect to storage backend")
	}

	var e *Engine
	readyFn := func(ctx context.Context) error {
		if e != nil && !e.readiness.Load() {
			return errors.New("engine is shutting down")
		}

		return primaryPool.Ping()
	}
	server := httpx.NewServer(&httpx.Config{ListenAddr: cfg.ListenAddr}, readyFn)

	startedAt := time.Now()
	server.SetHealthFn(func(ctx context.Context) httpx.HealthReport {
		checks := make(map[string]httpx.CheckResult)

		checks["postgres_primary"] = httpx.ProbeCheck(ctx, func(ctx context.Context) error {
			return primaryPool.Ping()
		})
		checks["postgres_replica"] = httpx.ProbeCheck(ctx, func(ctx context.Context) error {
			if replicaPool == nil {
				return nil
			}
			return replicaPool.Ping()
		})
		checks["redis"] = httpx.ProbeCheck(ctx, func(ctx context.Context) error {
			return cacheClient.Ping(ctx)
		})
		checks["meilisearch"] = httpx.ProbeCheck(ctx, func(ctx context.Context) error {
			if searchClient == nil {
				return nil
			}
			return searchClient.Ping()
		})
		checks["object_storage"] = httpx.ProbeCheck(ctx, func(ctx context.Context) error {
			if storageBackend == nil {
				return nil
			}
			_, err := storageBackend.Exists(ctx, "healthcheck")
			return err
		})

		status := "healthy"
		for _, c := range checks {
			if c.Status != "ok" {
				status = "degraded"
				break
			}
		}

		return httpx.HealthReport{
			Status:        status,
			Version:       "dev",
			UptimeSeconds: int64(time.Since(startedAt).Seconds()),
			Checks:        checks,
		}
	})

	runtime, err := wasm.New(cfg)
	if err != nil {
		_ = cacheClient.Close()
		_ = primaryPool.Close()
		_ = schemaPool.Close()
		if replicaPool != nil {
			_ = replicaPool.Close()
		}

		return nil, fmt.Errorf("create wasm runtime: %w", err)
	}

	e = &Engine{
		cfg:            cfg,
		wasmRuntime:    runtime,
		syncPool:       syncPool,
		tenantStore:    tenantStore,
		secretsBackend: secretsBackend,
		primaryDB:      primaryPool,
		replicaDB:      replicaPool,
		cacheClient:    cacheClient,
		searchClient:   searchClient,
		storageBackend: storageBackend,
		server:         server,
		adminServer:    adminServer,
	}

	return e, nil
}

func (e *Engine) Start(ctx context.Context) error {

	go func() {
		if err := e.server.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("http server error")
		}
	}()

	go func() {
		if err := e.adminServer.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("admin http server error")
		}
	}()

	e.readiness.Store(true)

	return nil
}

func (e *Engine) Shutdown(ctx context.Context) error {
	log.Info().Msg("shutdown initiated")

	e.readiness.Store(false)

	if e.cfg.ShutdownDrainDelay > 0 {
		time.Sleep(e.cfg.ShutdownDrainDelay)
	}

	if err := e.adminServer.Shutdown(ctx); err != nil {
		log.Warn().Err(err).Msg("could not shut down admin server")
	}

	if err := e.wasmRuntime.Close(ctx); err != nil {
		log.Warn().Err(err).Msg("could not close wasm runtime")
	}

	return nil
}

func newModuleContext(ctx context.Context, req EngineRequest, mod *module.LoadedModule) *wasm.ModuleContext {
	return wasm.NewModuleContext(req.ID, req.UserID, "", nil, req.TenantID, req.TenantSlug, req.TraceID, mod.Capabilities)
}

func (e *Engine) invokeHandler(
	ctx context.Context,
	inst *wasm.ModuleInstance,
	handlerName string,
	req EngineRequest,
	mod *module.LoadedModule,
) (EngineResponse, error) {
	moduleCtx := newModuleContext(ctx, req, mod)
	inst.SetModuleContext(moduleCtx)
	e.registerInstance(inst)
	defer func() {
		e.unregisterInstance(inst)
		for _, tx := range moduleCtx.OpenTransactions() {
			if err := tx.Rollback(); err != nil {
				log.Warn().Err(err).Msg("could not roll back transaction left open by module handler")
			}
		}
		inst.SetModuleContext(nil)
	}()

	reqBytes, err := msgpack.Marshal(req)
	if err != nil {
		return EngineResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	respBytes, err := inst.InvokeHandleRequest(ctx, reqBytes)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return EngineResponse{}, fmt.Errorf("handler %s: %w", handlerName, ctxErr)
		}
		return EngineResponse{}, fmt.Errorf("handler %s trapped: %w", handlerName, err)
	}

	var resp EngineResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		return EngineResponse{}, fmt.Errorf("unmarshal response: %w", err)
	}

	return resp, nil
}

func (e *Engine) registerInstance(inst *wasm.ModuleInstance) {
	e.instancesMu.Lock()
	defer e.instancesMu.Unlock()

	if e.instances == nil {
		e.instances = make(map[api.Module]*wasm.ModuleInstance)
	}
	e.instances[inst.Module()] = inst
}

func (e *Engine) unregisterInstance(inst *wasm.ModuleInstance) {
	e.instancesMu.Lock()
	defer e.instancesMu.Unlock()

	delete(e.instances, inst.Module())
}

func (e *Engine) instanceForModule(m api.Module) *wasm.ModuleInstance {
	e.instancesMu.Lock()
	defer e.instancesMu.Unlock()

	return e.instances[m]
}
