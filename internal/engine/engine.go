// Package engine constructs and runs the platform services, module runtime, and HTTP servers.
package engine

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/activitytype"
	"github.com/djangbahevans/goerp/internal/engine/adminapi"
	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auditlog"
	"github.com/djangbahevans/goerp/internal/engine/auth/acceptinvite"
	"github.com/djangbahevans/goerp/internal/engine/auth/adminconnectors"
	"github.com/djangbahevans/goerp/internal/engine/auth/adminmodules"
	"github.com/djangbahevans/goerp/internal/engine/auth/adminroles"
	"github.com/djangbahevans/goerp/internal/engine/auth/adminsettings"
	"github.com/djangbahevans/goerp/internal/engine/auth/adminusers"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authlogout"
	"github.com/djangbahevans/goerp/internal/engine/auth/authme"
	"github.com/djangbahevans/goerp/internal/engine/auth/authmepassword"
	"github.com/djangbahevans/goerp/internal/engine/auth/authmeupdate"
	"github.com/djangbahevans/goerp/internal/engine/auth/authrefresh"
	"github.com/djangbahevans/goerp/internal/engine/auth/authregister"
	"github.com/djangbahevans/goerp/internal/engine/auth/authsessions"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/connectorprimary"
	"github.com/djangbahevans/goerp/internal/engine/auth/emailverify"
	"github.com/djangbahevans/goerp/internal/engine/auth/handoff"
	"github.com/djangbahevans/goerp/internal/engine/auth/ipallowlist"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginflow"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfaenroll"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfafactors"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfareset"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfareverify"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfaverify"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/passwordreset"
	"github.com/djangbahevans/goerp/internal/engine/auth/planchange"
	"github.com/djangbahevans/goerp/internal/engine/auth/roleassign"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionpolicy"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/auth/tenantcontext"
	"github.com/djangbahevans/goerp/internal/engine/auth/tenantselect"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/connectoringress"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/eventdelivery"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/hotreload"
	"github.com/djangbahevans/goerp/internal/engine/httpx"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/jobdispatch"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/l10n/tenantl10n"
	"github.com/djangbahevans/goerp/internal/engine/mailer"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/mfa/lockout"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/revoke"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	"github.com/djangbahevans/goerp/internal/engine/mfa/webauthn"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/moduleboot"
	"github.com/djangbahevans/goerp/internal/engine/moduleinstall"
	"github.com/djangbahevans/goerp/internal/engine/modulereload"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notify"
	"github.com/djangbahevans/goerp/internal/engine/operatorcert"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/poolwarm"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/internal/engine/recordshares"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/savedfilters"
	"github.com/djangbahevans/goerp/internal/engine/scheduledactivity"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/search"
	"github.com/djangbahevans/goerp/internal/engine/searchindex"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/shellassets"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/storageupload"
	"github.com/djangbahevans/goerp/internal/engine/systemworker"
	"github.com/djangbahevans/goerp/internal/engine/telemetry"
	"github.com/djangbahevans/goerp/internal/engine/temporal"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantexport "github.com/djangbahevans/goerp/internal/engine/tenant/export"
	tenantimport "github.com/djangbahevans/goerp/internal/engine/tenant/import"
	tenantoffboard "github.com/djangbahevans/goerp/internal/engine/tenant/offboard"
	tenantprovision "github.com/djangbahevans/goerp/internal/engine/tenant/provision"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	tenantsync "github.com/djangbahevans/goerp/internal/engine/tenant/sync"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/vaultpki"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/webhookingress"
	"github.com/djangbahevans/goerp/internal/engine/workflowworker"
	"github.com/djangbahevans/goerp/internal/engine/ws"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type Engine struct {
	cfg         *config.Config
	wasmRuntime *wasm.Runtime

	syncPool          *schema.SchemaSyncPool
	tenantStore       *tenant.Store
	sessionStore      *session.Store
	signingKeySet     *signingkey.SigningKeySet
	tokenIssuer       *authtoken.Issuer
	sessionRevoker    *sessionrevoke.Revoker
	authChecker       *authcheck.Checker
	tenantResolver    *tenantresolve.Resolver
	moduleRegistry    *registry.ModuleRegistry
	rolePermissionMap *permcache.RolePermissionMap
	jobQueue          *river.Client[pgx.Tx]
	jobQueuePool      *pgxpool.Pool

	tracer         trace.Tracer
	tracerProvider *sdktrace.TracerProvider

	secretsBackend      secrets.Backend
	primaryDB           *sql.DB
	replicaDB           *sql.DB
	userStore           *user.Store
	recordSharesStore   *recordshares.Store
	savedFiltersStore   *savedfilters.Store
	recordActivityStore *recordactivity.Store
	notificationStore   *notifications.Store
	notificationConfig  *notifconfig.Service
	notifier            engineNotifier
	// txJobs inserts the engine's own jobs inside a database/sql
	// transaction, such as a comment's notification job
	// (record-activity.md §10). Nil in tests that enqueue nothing.
	txJobs           txJobInserter
	unsubscribeCodec *notifications.UnsubscribeCodec
	// tenantLocales resolves a tenant's default locale and timezone, for
	// work outside a request such as due-date reminders.
	tenantLocales          *tenantl10n.Store
	scheduledActivityStore *scheduledactivity.Store
	activityTypeStore      *activitytype.Store
	// roleStore resolves another user's tenant roles when a route reads a
	// record with their permissions (record-activity.md §7).
	roleStore       *role.Store
	filesStore      *files.Store
	cacheClient     *cache.Client
	searchClient    *search.Client
	storageBackend  storage.Backend
	temporalClient  *temporal.Client
	workflowWorkers *workflowworker.Manager
	systemWorker    *systemworker.Worker
	server          *httpx.Server
	adminServer     *adminapi.Server
	readiness       atomic.Bool
	serveErrs       chan error
	wsHub           *ws.Hub

	// instanceID identifies this process for hot reload's leader-election
	// lock value (docs/engine-internals.md §10) — generated once per
	// process, not persisted or configurable.
	instanceID string
	hotReload  *hotreload.Coordinator

	tenantConfigListener *tenantconfig.Listener
	rolesListener        *permcache.Listener
}

func New(cfg *config.Config) (*Engine, error) {
	var shell *shellassets.Handler
	if cfg.ShellDir != "" {
		var err error
		shell, err = shellassets.New(os.DirFS(cfg.ShellDir))
		if err != nil {
			return nil, fmt.Errorf("load shell build: %w", err)
		}
	}

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

	closeDBs := func() {
		_ = primaryPool.Close()
		_ = schemaPool.Close()
		if replicaPool != nil {
			_ = replicaPool.Close()
		}
	}

	syncPool := schema.NewPool(schemaPool, 30*time.Second)
	if err := bootstrapSystemSchema(ctx, schemaPool, syncPool); err != nil {
		closeDBs()
		return nil, err
	}

	tenantStore := tenant.NewStore(primaryPool)
	tenantStore.AddReservedSlugs(cfg.ReservedSlugs...)

	billingStore := billing.NewStore(primaryPool)

	checkpointStore := checkpoint.NewStore(primaryPool)

	userStore := user.NewStore(primaryPool)
	recordSharesStore := recordshares.NewStore(primaryPool)
	savedFiltersStore := savedfilters.NewStore(primaryPool)
	recordActivityStore := recordactivity.NewStore(primaryPool)
	scheduledActivityStore := scheduledactivity.NewStore(primaryPool)
	activityTypeStore := activitytype.NewStore(primaryPool)

	apiKeyStore := apikey.NewStore(primaryPool)

	mfaStore := mfa.NewStore(primaryPool)

	rowCryptStore := rowcrypt.NewStore(primaryPool, secretsBackend)
	rowKeySet, err := rowCryptStore.LoadOrGenerate(ctx)
	if err != nil {
		closeDBs()
		return nil, fmt.Errorf("load row encryption key: %w", err)
	}

	tenantConfigStore := tenantconfig.NewStore(primaryPool)

	// roleStore's tables are per-tenant (roles/role_permissions/user_roles
	// live in each tenant's own schema, not system), created by
	// provisioning (goerp#149), not here at engine startup.
	roleStore := role.NewStore(primaryPool)
	inviteMailer := mailer.New(mailer.Config{
		Host:           cfg.SMTPHost,
		Port:           cfg.SMTPPort,
		User:           cfg.SMTPUser,
		Pass:           cfg.SMTPPass,
		From:           cfg.SMTPFrom,
		BaseURL:        cfg.AppBaseURL,
		PlatformDomain: cfg.PlatformDomain,
	})
	authAuditStore := authaudit.NewStore(primaryPool, tenantStore)
	inviteStore := invite.NewStore(primaryPool, userStore, roleStore, authAuditStore, inviteMailer)

	auditStore := auditlog.NewStore(primaryPool)
	operatorCertStore := operatorcert.NewStore(primaryPool)
	sessionStore := session.NewStore(primaryPool)

	signingKeyStore := signingkey.NewStore(primaryPool, secretsBackend)
	signingKeySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		closeDBs()
		return nil, fmt.Errorf("load jwt signing key: %w", err)
	}

	tokenIssuer := authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore)
	sessionPolicies := sessionpolicy.NewStore(tenantConfigStore)
	tokenIssuer.SetSessionPolicies(sessionPolicies)
	ipAllowlists := ipallowlist.NewStore(tenantConfigStore)
	tokenIssuer.SetIPAllowlists(ipAllowlists)

	mfaTokenKeyStore := mfatoken.NewStore(primaryPool, secretsBackend)
	mfaTokenKeySet, err := mfaTokenKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		closeDBs()
		return nil, fmt.Errorf("load mfa token signing key: %w", err)
	}

	mfaTokenCodec := mfatoken.NewCodec(&mfaTokenKeySet.Active)

	// PKI issuance/revocation only makes sense with a real PKI backend
	// behind it; stays nil (routes report StatusNotImplemented) for any
	// other GOERP_SECRETS_BACKEND.
	var operatorPKI adminapi.OperatorPKI
	if cfg.SecretsBackend == "vault" {
		pki, err := vaultpki.New()
		if err != nil {
			log.Warn().Err(err).Msg("could not create vault pki client, operator cert issuance/revocation disabled")
		} else {
			operatorPKI = pki
		}
	}

	adminServer, err := adminapi.NewServer(&adminapi.Config{
		ListenAddr:    cfg.AdminAddr,
		AdminToken:    adminToken,
		MaxBodyBytes:  cfg.AdminMaxBodyBytes,
		MaxConcurrent: cfg.AdminMaxConcurrent,
		AuditStore:    auditStore,
	})
	if err != nil {
		closeDBs()
		return nil, fmt.Errorf("create admin server: %w", err)
	}

	adminapi.RegisterOperatorsRoutes(adminServer.Router(), adminapi.OperatorsDeps{
		PKI:    operatorPKI,
		Ledger: operatorCertStore,
	})

	cacheClient, err := cache.New(ctx, cache.Config{
		Addr:          cfg.RedisAddr,
		MasterName:    cfg.RedisSentinelMaster,
		SentinelAddrs: cfg.RedisSentinelAddrs,
		DB:            cfg.RedisDB,
		MaxRetries:    cfg.RedisMaxRetries,
	})
	if err != nil {
		closeDBs()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}

	notificationStore := notifications.NewStore(primaryPool).WithCache(cacheClient)

	temporalClient, err := temporal.New(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("could not connect to temporal")
	}

	sessionRevoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)

	roleCache := permcache.NewRoleCache(cacheClient)

	tenantResolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)

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

	// workflowWorkers is constructed here but only spawns processes in
	// Start (Stage 6 step 30 runs after step 29's River start) — the same
	// "client built in New, started in Start" split jobQueue and
	// temporalClient already use.
	workflowWorkers := workflowworker.NewManager(storageBackend, temporalClient, filepath.Join(cfg.ModuleDir, ".workflow-worker-cache"))

	systemWorker := systemworker.New(temporalClient)

	var e *Engine
	readyFn := func(ctx context.Context) error {
		if e != nil && !e.readiness.Load() {
			return errors.New("engine is shutting down")
		}

		return primaryPool.Ping()
	}

	server := httpx.NewServer(&httpx.Config{
		ListenAddr:        cfg.ListenAddr,
		ReadTimeout:       cfg.ServerReadTimeout,
		ReadHeaderTimeout: cfg.ServerReadHeaderTimeout,
		WriteTimeout:      cfg.ServerWriteTimeout,
		IdleTimeout:       cfg.ServerIdleTimeout,
		MaxHeaderBytes:    cfg.ServerMaxHeaderBytes,
		TLSCertFile:       cfg.TLSCertFile,
		TLSKeyFile:        cfg.TLSKeyFile,
	}, http.NotFoundHandler(), readyFn)

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
		checks["temporal"] = httpx.ProbeCheck(ctx, func(ctx context.Context) error {
			if temporalClient == nil {
				return nil
			}

			return temporalClient.Ping(ctx)
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

	runtime, err := wasm.New(cfg, primaryPool, storageBackend, cacheClient)
	if err != nil {
		_ = cacheClient.Close()
		closeDBs()

		return nil, fmt.Errorf("create wasm runtime: %w", err)
	}

	// replicaPool is warn-only (nil on a failed connect, per Stage 1 above)
	// — SetReplicaDB tolerates that, and host.db.query/query_replica's own
	// nil-guard turns a replica-requiring call into db.replica_unavailable
	// rather than a nil-pointer panic.
	runtime.SetReplicaDB(replicaPool)
	runtime.SetSchemaSyncDB(schemaPool)
	// rowKeySet was already loaded above (needed before totp.Service could
	// decrypt an enrolled TOTP secret) — host.config's own encrypted
	// config_schema entries (host-abi-reference.md §14) reuse the same
	// key set rather than a second AES-256-GCM implementation.
	runtime.SetRowCryptKeys(rowKeySet)

	// Telemetry setup happens here, immediately before closeOnFailure is
	// first defined, rather than at the top of New() — SetupTracing opens
	// a live gRPC connection and starts a background export goroutine
	// on success, and every earlier fail-hard bootstrap step above
	// already returns directly (nothing existed yet for it to clean up);
	// constructing the tracer any earlier would leak that connection on
	// each of those return paths, since none of them know to shut it
	// down. Failure here is warn-only, matching every other observability
	// dependency's posture — this is optional infrastructure, not a
	// Stage 1 fail-hard dependency (engine-internals.md §2).
	tracerProvider, tracer, err := telemetry.SetupTracing(ctx, telemetry.Config{
		Endpoint:    cfg.OTelExporterOTLPEndpoint,
		ServiceName: cfg.OTelServiceName,
		Environment: cfg.Environment,
		Insecure:    cfg.OTelInsecure,
	})
	if err != nil {
		log.Warn().Err(err).Msg("could not initialize OpenTelemetry tracer provider, falling back to no-op tracer")
	}

	closeOnFailure := func() {
		if tracerProvider != nil {
			_ = tracerProvider.Shutdown(ctx)
		}

		_ = runtime.Close(ctx)
		_ = cacheClient.Close()
		closeDBs()
	}

	sources, err := moduleboot.Discover(cfg.ModuleDir)
	if err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("discover module sources: %w", err)
	}

	ordered, err := moduleboot.Order(sources)
	if err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("order module dependencies: %w", err)
	}

	poolCfg := wasm.PoolConfig{
		WarmSize:      cfg.PoolWarmSize,
		MaxSize:       cfg.PoolMaxSize,
		BorrowTimeout: cfg.PoolBorrowTimeout,
	}

	loadedModules := moduleboot.LoadCascading(ctx, runtime, poolCfg, storageBackend, ordered)

	moduleRegistry := &registry.ModuleRegistry{}
	eventInvoker := &eventdelivery.HandlerInvoker{Runtime: runtime, TenantStore: tenantStore, Roles: roleStore}
	runtime.SetSyncEventDispatcher(&eventdelivery.SyncDispatcher{ModuleRegistry: moduleRegistry, Invoker: eventInvoker})
	runtime.SetSyncJobDispatcher(&jobdispatch.SyncDispatcher{
		ModuleRegistry: moduleRegistry,
		Runtime:        runtime,
		Roles:          roleStore,
	})
	runtime.SetProviderStore(providerselect.NewStore(primaryPool))
	snap, err := moduleRegistry.Update(loadedModules)
	if err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("publish module registry: %w", err)
	}

	// permcache.RolePermissionMap (auth-internals.md §14 cache layer 3)
	// has to be rebuilt in lockstep with the permission registry above —
	// a stale map would resolve role bitfields against index assignments
	// that no longer match modulePerms. moduleinstall.Worker is the only
	// other caller of registry.Update anywhere in the engine, and it
	// rebuilds this same map itself right after its own Update call, for
	// the identical reason.
	rolePermissionMap := permcache.NewRolePermissionMap()
	if err := rolePermissionMap.RebuildAll(ctx, tenantStore, roleStore, snap.PermissionRegistry()); err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("build role permission map: %w", err)
	}

	tenantConfigResolver := tenantconfig.NewResolver(tenantConfigStore, tenantStore, moduleRegistry)
	tenantConfigListener := tenantconfig.NewListener(primaryPool, tenantConfigResolver)
	// Wires host.config.get/set (host_config.go) to the same resolver and
	// store the Listener above keeps cache-fresh across replicas —
	// moduleRegistry (Resolver's own manifest-default fallback) only
	// exists from this point on in New, so this can't happen any earlier,
	// same reasoning as SetSyncEventDispatcher just above.
	runtime.SetTenantConfig(tenantConfigResolver, tenantConfigStore)
	connectorIngressStore := connectoringress.NewStore(primaryPool)
	runtime.SetConnectorInbox(connectorIngressStore)
	webhookIngressHandler := webhookingress.NewHandler(webhookingress.Deps{
		Connector: func(moduleName string) (webhookingress.Connector, bool, bool) {
			snap := moduleRegistry.Snapshot()
			if snap == nil {
				return webhookingress.Connector{}, false, false
			}
			return webhookingress.ConnectorOf(snap.Modules()[moduleName])
		},
		Endpoints: connectorIngressStore,
		Inbox:     connectorIngressStore,
		Tenants:   tenantStore,
		Config:    tenantConfigResolver,
		Decrypt:   rowKeySet.Decrypt,
		Verifier:  runtime,
		Enqueuer:  runtime,
		Redis:     cacheClient,
	})
	notificationConfig := notifconfig.NewService(tenantConfigResolver, tenantConfigStore, rowKeySet)

	// Rebuilds a tenant's rolePermissionMap entries on this replica when a
	// tenant admin changes roles on any replica (auth/adminroles).
	currentPermissionRegistry := func() *permission.PermissionRegistry {
		if snap := moduleRegistry.Snapshot(); snap != nil && snap.PermissionRegistry() != nil {
			return snap.PermissionRegistry()
		}

		return permission.NewPermissionRegistry()
	}

	rolesListener := permcache.NewListener(primaryPool, tenantStore, roleStore, currentPermissionRegistry, rolePermissionMap)

	mfaPolicyStore := enforce.NewStore(tenantConfigStore)
	authChecker := authcheck.NewChecker(&signingKeySet.Active, sessionRevoker, userStore, roleStore, roleCache, rolePermissionMap, apiKeyStore, cfg.EnableAPIKeys, mfaTokenCodec, mfaStore, mfaPolicyStore)

	adminapi.RegisterActivityDispatchRoute(adminServer.UnauthenticatedRouter(), adminapi.ActivityDispatchDeps{
		Registry:    moduleRegistry,
		Tenants:     tenantStore,
		Roles:       roleStore,
		Runtime:     runtime,
		Credentials: workflowWorkers,
	})

	server.SetModulesFn(func() (httpx.ModulesReport, []httpx.FailedModule) {
		snap := moduleRegistry.Snapshot()
		if snap == nil {
			return httpx.ModulesReport{}, nil
		}

		report := httpx.ModulesReport{}
		var failed []httpx.FailedModule
		for name, m := range snap.Modules() {
			report.Total++
			if m.Status == module.StatusFailed {
				report.Failed++
				failed = append(failed, httpx.FailedModule{Name: name, Reason: m.FailureReason})
				continue
			}

			report.Ready++
		}

		return report, failed
	})

	passwordPolicies := password.NewPolicyStore(tenantConfigStore, roleStore)
	passwordHasher := password.NewHasher(cfg.Argon2MemoryBudgetMB, cfg.Argon2AcquireTimeout)
	handoffStore := handoff.NewStore(cacheClient, tenantResolver, cfg.PlatformDomain)
	registerHandlers := authregister.NewHandlers(
		authregister.Config{
			Enabled:            cfg.RegistrationEnabled,
			VerificationPolicy: cfg.RequireEmailVerification,
			ProvisionTimeout:   max(cfg.ServerWriteTimeout-10*time.Second, 5*time.Second),
		},
		userStore, tenantStore, tenantprovision.NewProvisioner(temporalClient, systemworker.TaskQueue),
		passwordHasher, tokenIssuer, inviteMailer, handoffStore,
	)
	acceptInviteHandlers := acceptinvite.NewHandlers(tenantStore, inviteStore, userStore, passwordPolicies, passwordHasher, tokenIssuer)
	loginHandler := loginflow.NewHandler(userStore, tenantStore, roleStore, mfaStore, tokenIssuer, mfaTokenCodec, passwordPolicies, passwordHasher, cacheClient, authAuditStore, tenantResolver, handoffStore, tenantselect.NewStore(cacheClient), ipAllowlists)
	totpService := totp.NewService(mfaStore, rowKeySet, cacheClient)
	recoveryCodeService := recoverycode.NewService(mfaStore)
	passkeyRPID := cfg.WebAuthnRPID
	if passkeyRPID == "" {
		passkeyRPID = cfg.PlatformDomain
	}
	passkeyService, err := webauthn.NewService(webauthn.Config{RPID: passkeyRPID, RPDisplayName: cfg.WebAuthnRPDisplayName, RPOrigins: cfg.WebAuthnRPOrigins, BaseURL: cfg.AppBaseURL}, mfaStore, rowKeySet, cacheClient, sessionStore)
	if err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("configure passkeys: %w", err)
	}

	mfaVerifyHandler := mfaverify.NewHandler(mfaTokenCodec, cacheClient, totpService, recoveryCodeService, tenantStore, tokenIssuer, mfaStore, passkeyService, authAuditStore)
	mfaLockout := lockout.NewCounter(cacheClient)
	mfaReverifyHandler := mfareverify.NewHandler(tenantResolver, authChecker, sessionStore, tokenIssuer, totpService, recoveryCodeService, mfaLockout, mfaStore, passkeyService, authAuditStore)
	mfaEnrollHandlers := mfaenroll.NewHandlers(tenantResolver, authChecker, userStore, mfaStore, sessionStore, tokenIssuer, totpService, recoveryCodeService, authAuditStore, passkeyService)
	mfaFactorHandlers := mfafactors.NewHandlers(tenantResolver, authChecker, mfaStore, mfaPolicyStore, totpService, recoveryCodeService, mfaLockout, revoke.NewService(mfaStore, sessionRevoker), sessionRevoker, authAuditStore)
	mfaResetHandler := mfareset.NewHandler(tenantResolver, authChecker, userStore, roleStore, mfaStore, sessionRevoker, inviteMailer, authAuditStore, passwordHasher)
	passwordResetRequestHandler := passwordreset.NewRequestHandler(userStore, tenantStore, roleStore, cacheClient, inviteMailer, authAuditStore)
	passwordResetConfirmHandler := passwordreset.NewConfirmHandler(userStore, tenantStore, roleStore, mfaStore, sessionRevoker, tokenIssuer, passwordPolicies, inviteMailer, authAuditStore, passwordHasher)
	verifyEmailConfirmHandler := emailverify.NewConfirmHandler(userStore, tenantStore, roleStore, mfaStore, tokenIssuer)
	verifyEmailResendHandler := emailverify.NewResendHandler(userStore, tenantStore, roleStore, cacheClient, inviteMailer)
	filesStore := files.NewStore(primaryPool)
	tenantLocales := tenantl10n.NewStore(tenantConfigStore, cfg.AvailableLocales)
	authMeHandler := authme.NewHandler(tenantResolver, authChecker, userStore, roleStore, filesStore, storageBackend, tenantLocales, passwordPolicies)
	authMeUpdateHandler := authmeupdate.NewHandler(tenantResolver, authChecker, userStore, roleStore, filesStore, tenantLocales)
	authMePasswordHandler := authmepassword.NewHandler(tenantResolver, authChecker, userStore, passwordPolicies, sessionRevoker, sessionStore, tokenIssuer, inviteMailer, authAuditStore, passwordHasher)
	authRefreshHandler := authrefresh.NewHandler(tokenIssuer)
	authLogoutHandler := authlogout.NewHandler(tenantResolver, authChecker, sessionRevoker)
	authSessionsHandler := authsessions.NewHandler(tenantResolver, authChecker, sessionStore, sessionRevoker, authAuditStore)
	tenantContextHandler := tenantcontext.NewHandler(tenantResolver, tenantcontext.Config{RegistrationEnabled: cfg.RegistrationEnabled, TermsURL: cfg.TermsURL, AppBaseURL: cfg.AppBaseURL, Policies: passwordPolicies})
	storageUploadHandler := storageupload.NewHandler(tenantResolver, authChecker, storageBackend, filesStore, storageupload.Limits{
		MaxFileBytes: cfg.StorageMaxFileBytes,
		AllowedTypes: cfg.StorageAllowedTypes,
		BlockedTypes: cfg.StorageBlockedTypes,
	})
	builtinRoutes := map[string]http.Handler{
		"GET /_health":                             server.HealthHandler(),
		"GET /_ready":                              server.ReadyHandler(),
		"GET /auth/me":                             authMeHandler,
		"POST /auth/me/change-password":            authMePasswordHandler,
		"PATCH /auth/me":                           authMeUpdateHandler,
		"POST /auth/refresh":                       authRefreshHandler,
		"POST /auth/register":                      http.HandlerFunc(registerHandlers.Register),
		"GET /auth/check-slug":                     http.HandlerFunc(registerHandlers.CheckSlug),
		"GET /auth/accept-invite/info":             http.HandlerFunc(acceptInviteHandlers.Info),
		"POST /auth/accept-invite":                 http.HandlerFunc(acceptInviteHandlers.Accept),
		"POST /auth/login":                         loginHandler,
		"POST /auth/handoff":                       http.HandlerFunc(loginHandler.ServeHandoff),
		"POST /auth/select-tenant":                 http.HandlerFunc(loginHandler.ServeSelectTenant),
		"POST /auth/password-reset/request":        passwordResetRequestHandler,
		"POST /auth/password-reset/confirm":        passwordResetConfirmHandler,
		"POST /auth/verify-email":                  verifyEmailConfirmHandler,
		"POST /auth/verify-email/resend":           verifyEmailResendHandler,
		"GET /auth/tenant-context":                 tenantContextHandler,
		"POST /_webhooks/{module_name}/{token}":    webhookIngressHandler,
		"POST /auth/logout":                        authLogoutHandler,
		"GET /auth/sessions":                       http.HandlerFunc(authSessionsHandler.ServeList),
		"DELETE /auth/sessions":                    http.HandlerFunc(authSessionsHandler.ServeRevokeOthers),
		"DELETE /auth/sessions/{family_id}":        http.HandlerFunc(authSessionsHandler.ServeRevoke),
		"POST /auth/mfa/verify":                    mfaVerifyHandler,
		"POST /auth/mfa/reverify":                  mfaReverifyHandler,
		"POST /auth/mfa/enroll/totp":               http.HandlerFunc(mfaEnrollHandlers.Begin),
		"POST /auth/mfa/enroll/totp/confirm":       http.HandlerFunc(mfaEnrollHandlers.Confirm),
		"POST /auth/mfa/enroll/webauthn":           http.HandlerFunc(mfaEnrollHandlers.BeginWebAuthn),
		"POST /auth/mfa/enroll/webauthn/confirm":   http.HandlerFunc(mfaEnrollHandlers.ConfirmWebAuthn),
		"POST /auth/mfa/webauthn/options":          http.HandlerFunc(mfaVerifyHandler.Options),
		"POST /auth/mfa/reverify/webauthn/options": http.HandlerFunc(mfaReverifyHandler.Options),
		"GET /auth/mfa/factors":                    http.HandlerFunc(mfaFactorHandlers.List),
		"POST /auth/mfa/factors/{id}/remove":       http.HandlerFunc(mfaFactorHandlers.Remove),
		"POST /auth/mfa/recovery-codes/regenerate": http.HandlerFunc(mfaFactorHandlers.RegenerateRecoveryCodes),
		"POST /admin/users/{id}/mfa/reset":         mfaResetHandler,
		"POST /storage/upload":                     storageUploadHandler,
	}

	defaultRateLimit := route.RateLimitConfig{Requests: cfg.RateLimitMax, WindowSeconds: int(cfg.RateLimitWindow.Seconds()), Scope: "ip"}

	orderedModules := make([]*module.LoadedModule, len(ordered))
	for i, src := range ordered {
		orderedModules[i] = loadedModules[src.Name]
	}

	diffEngine := schema.NewSchemaDiffEngine(&schema.Config{DDLStatementTimeout: cfg.SchemaSyncDDLStatementTimeout, ModelSource: moduleRegistry})
	if err := tenantsync.SyncEngineNotificationTemplates(ctx, syncPool, tenantStore, cfg.SchemaSyncConcurrency); err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("sync engine notification templates: %w", err)
	}

	if err := tenantsync.SyncAll(ctx, syncPool, diffEngine, tenantStore, orderedModules, cfg.SchemaSyncConcurrency); err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("sync tenant schemas: %w", err)
	}

	// ProvisionTenantWorkflow's activities need moduleRegistry/diffEngine,
	// which don't exist until here — registered on systemWorker (built
	// earlier, alongside temporalClient) now, started later in Start.
	provisionActivities := tenantprovision.NewActivities(tenantStore, inviteStore, roleStore, schemaPool, syncPool, diffEngine, moduleRegistry, cfg.PlatformDomain, cacheClient, cfg.AvailableLocales)
	systemWorker.RegisterWorkflow(tenantprovision.Workflow)
	systemWorker.RegisterActivity(provisionActivities)

	// OffboardTenantWorkflow's activities need moduleRegistry too (its
	// DeleteSearchIndexes step enumerates each loaded module's declared
	// SearchIndexes) — registered here for the same reason
	// provisionActivities is. filesStore (constructed above, alongside
	// storageUploadHandler) is DeleteTenantStorageFiles's one reader.
	offboardActivities := tenantoffboard.NewActivities(tenantStore, filesStore, cacheClient, searchClient, storageBackend, schemaPool, moduleRegistry)
	systemWorker.RegisterWorkflow(tenantoffboard.OffboardTenantWorkflow)
	systemWorker.RegisterActivity(offboardActivities)

	poolwarm.WarmAll(ctx, loadedModules)

	// jobQueuePool is a separate pool from primaryPool: river's pgx driver
	// (riverpgxv5) needs a native *pgxpool.Pool, while every other store in
	// this file takes the database/sql-wrapped *sql.DB primaryPool returns.
	// Both point at the same DSN. Using pgx here instead of river's generic
	// database/sql driver (riverdatabasesql) avoids a transitive dependency
	// on github.com/lib/pq, which govulncheck flags for advisories with no
	// fixed release — the engine never actually opens a pq connection
	// (db.go uses pgx/v5/stdlib), but riverdatabasesql pulls the package in
	// regardless.
	jobQueuePool, err := db.NewPgxPool(ctx, cfg.DBPrimaryDSN)
	if err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("connect job queue pool: %w", err)
	}

	closeOnFailure = func() {
		if tracerProvider != nil {
			_ = tracerProvider.Shutdown(ctx)
		}

		jobQueuePool.Close()
		_ = runtime.Close(ctx)
		_ = cacheClient.Close()
		closeDBs()
	}

	// River's migrations are DDL, so they run as the schema-sync role, which
	// owns River's tables; jobQueuePool's role only has DML on them.
	if err := migrateJobQueue(ctx, cfg.DBSchemaSyncDSN); err != nil {
		closeOnFailure()
		return nil, err
	}

	jobWorkers := river.NewWorkers()
	river.AddWorker(jobWorkers, &jobqueue.ProbeWorker{})
	river.AddWorker(jobWorkers, &schema.ValidateConstraintWorker{Pool: schemaPool})
	river.AddWorker(jobWorkers, &tenantoffboard.ImmediateWorker{Activities: offboardActivities, TenantStore: tenantStore})
	river.AddWorker(jobWorkers, &tenantexport.Worker{
		TenantStore:    tenantStore,
		Registry:       moduleRegistry,
		RawDB:          syncPool.Raw(),
		Checkpoints:    checkpointStore,
		StorageBackend: storageBackend,
		Keys:           rowKeySet,
	})
	river.AddWorker(jobWorkers, &tenantimport.Worker{
		TenantStore:    tenantStore,
		Registry:       moduleRegistry,
		RawDB:          syncPool.Raw(),
		Checkpoints:    checkpointStore,
		StorageBackend: storageBackend,
		Provision:      provisionActivities,
		Keys:           rowKeySet,
	})
	syncWorker := &tenantsync.SyncWorker{
		TenantStore: tenantStore,
		Registry:    moduleRegistry,
		Pool:        syncPool,
		DiffEngine:  diffEngine,
	}

	river.AddWorker(jobWorkers, syncWorker)
	acceptResyncWorker := &tenantsync.AcceptResyncWorker{
		TenantStore: tenantStore,
		Registry:    moduleRegistry,
		Pool:        syncPool,
		DiffEngine:  diffEngine,
	}

	river.AddWorker(jobWorkers, acceptResyncWorker)
	wsHub := ws.NewHub()
	// Added to builtinRoutes here rather than the literal above: unlike
	// mfaResetHandler, roleAssignHandler needs wsHub, which doesn't exist
	// until this point.
	roleAssignHandler := roleassign.NewHandler(tenantResolver, authChecker, roleStore, roleCache, sessionRevoker, wsHub, authAuditStore)
	builtinRoutes["POST /admin/users/{id}/roles"] = http.HandlerFunc(roleAssignHandler.ServeAssign)
	builtinRoutes["DELETE /admin/users/{id}/roles/{role}"] = http.HandlerFunc(roleAssignHandler.ServeRevoke)
	adminUsersHandler := adminusers.NewHandler(tenantResolver, authChecker, adminusers.NewStore(primaryPool, authAuditStore), roleStore, roleCache, sessionStore, sessionRevoker, inviteStore, userStore, filesStore, storageBackend, func(table string) (string, bool) {
		snap := moduleRegistry.Snapshot()
		if snap == nil {
			return "", false
		}

		return snap.ModelForTable(table)
	})
	builtinRoutes["GET /admin/users"] = http.HandlerFunc(adminUsersHandler.ServeList)
	builtinRoutes["GET /admin/users/{id}"] = http.HandlerFunc(adminUsersHandler.ServeGet)
	builtinRoutes["DELETE /admin/users/{id}"] = http.HandlerFunc(adminUsersHandler.ServeDelete)
	builtinRoutes["POST /admin/users/{id}/suspend"] = http.HandlerFunc(adminUsersHandler.ServeSuspend)
	builtinRoutes["POST /admin/users/{id}/unsuspend"] = http.HandlerFunc(adminUsersHandler.ServeUnsuspend)
	builtinRoutes["GET /admin/users/{id}/sessions"] = http.HandlerFunc(adminUsersHandler.ServeSessions)
	builtinRoutes["GET /admin/users/{id}/activity"] = http.HandlerFunc(adminUsersHandler.ServeActivity)
	builtinRoutes["DELETE /admin/users/{id}/sessions/{family_id}"] = http.HandlerFunc(adminUsersHandler.ServeRevokeSession)
	builtinRoutes["POST /users/invite"] = http.HandlerFunc(adminUsersHandler.ServeInvite)
	builtinRoutes["POST /users/invitations/{id}/resend"] = http.HandlerFunc(adminUsersHandler.ServeResendInvitation)
	builtinRoutes["POST /users/invitations/{id}/revoke"] = http.HandlerFunc(adminUsersHandler.ServeRevokeInvitation)
	adminRolesHandler := adminroles.NewHandler(tenantResolver, authChecker, roleStore, moduleRegistry, rolePermissionMap, sessionRevoker, wsHub, authAuditStore)
	builtinRoutes["GET /admin/roles"] = http.HandlerFunc(adminRolesHandler.ServeList)
	builtinRoutes["POST /admin/roles"] = http.HandlerFunc(adminRolesHandler.ServeCreate)
	builtinRoutes["GET /admin/roles/permissions"] = http.HandlerFunc(adminRolesHandler.ServePermissions)
	builtinRoutes["GET /admin/roles/{id}"] = http.HandlerFunc(adminRolesHandler.ServeGet)
	builtinRoutes["PATCH /admin/roles/{id}"] = http.HandlerFunc(adminRolesHandler.ServeUpdate)
	builtinRoutes["DELETE /admin/roles/{id}"] = http.HandlerFunc(adminRolesHandler.ServeDelete)
	planChangeHandler := planchange.NewHandler(tenantResolver, authChecker, billingStore, tenantStore, cacheClient, wsHub, authAuditStore)
	builtinRoutes["POST /admin/tenant/plan"] = http.HandlerFunc(planChangeHandler.ServeHTTP)
	adminSettingsHandler := adminsettings.NewHandler(adminsettings.Deps{
		Tenants:                 tenantResolver,
		Auth:                    authChecker,
		TenantStore:             tenantStore,
		Cache:                   cacheClient,
		Roles:                   roleStore,
		Config:                  tenantConfigStore,
		MFA:                     mfaPolicyStore,
		Passwords:               passwordPolicies,
		Sessions:                sessionPolicies,
		IPAllowlists:            ipAllowlists,
		Locales:                 tenantLocales,
		Audit:                   authAuditStore,
		Storage:                 storageBackend,
		Files:                   filesStore,
		MaxLogoBytes:            min(cfg.StorageMaxFileBytes, adminsettings.MaxLogoBytes),
		Notifications:           notificationConfig,
		EmailVerificationPolicy: cfg.RequireEmailVerification,
		Registry:                moduleRegistry,
		Users:                   userStore,
		TestEmail: &notify.EmailTester{
			Registry:              moduleRegistry,
			Tenants:               tenantStore,
			AppBaseURL:            cfg.AppBaseURL,
			PlatformDomain:        cfg.PlatformDomain,
			SMTPAllowPrivateHosts: cfg.NotificationSMTPAllowPrivateHosts,
		},
		Templates:      notificationStore,
		AppBaseURL:     cfg.AppBaseURL,
		PlatformDomain: cfg.PlatformDomain,
	})
	builtinRoutes["GET /admin/settings"] = http.HandlerFunc(adminSettingsHandler.ServeGet)
	builtinRoutes["PATCH /admin/settings"] = http.HandlerFunc(adminSettingsHandler.ServePatch)
	builtinRoutes["POST /admin/settings/logo"] = http.HandlerFunc(adminSettingsHandler.ServeUploadLogo)
	builtinRoutes["DELETE /admin/settings/logo"] = http.HandlerFunc(adminSettingsHandler.ServeDeleteLogo)
	builtinRoutes["GET /admin/settings/notification-delivery"] = http.HandlerFunc(adminSettingsHandler.ServeGetNotificationDelivery)
	builtinRoutes["PATCH /admin/settings/notification-delivery"] = http.HandlerFunc(adminSettingsHandler.ServePatchNotificationDelivery)
	builtinRoutes["POST /admin/settings/notification-delivery/test-email"] = http.HandlerFunc(adminSettingsHandler.ServeTestEmail)
	builtinRoutes["GET /admin/settings/notification-templates"] = http.HandlerFunc(adminSettingsHandler.ServeListNotificationTemplates)
	builtinRoutes["GET /admin/settings/notification-templates/{type}/{channel}/{locale}"] = http.HandlerFunc(adminSettingsHandler.ServeGetNotificationTemplate)
	builtinRoutes["PUT /admin/settings/notification-templates/{type}/{channel}/{locale}"] = http.HandlerFunc(adminSettingsHandler.ServePutNotificationTemplate)
	builtinRoutes["DELETE /admin/settings/notification-templates/{type}/{channel}/{locale}"] = http.HandlerFunc(adminSettingsHandler.ServeDeleteNotificationTemplate)
	builtinRoutes["POST /admin/settings/notification-templates/{type}/{channel}/{locale}/preview"] = http.HandlerFunc(adminSettingsHandler.ServePreviewNotificationTemplate)
	connectorPrimaryHandler := connectorprimary.NewHandler(tenantResolver, authChecker, providerselect.NewStore(primaryPool), authAuditStore)
	builtinRoutes["PATCH /admin/connectors/{name}/set-primary"] = http.HandlerFunc(connectorPrimaryHandler.ServeSetPrimary)
	adminConnectorsHandler := adminconnectors.NewHandler(adminconnectors.Deps{
		Tenants:   tenantResolver,
		Auth:      authChecker,
		Registry:  moduleRegistry,
		Config:    tenantConfigStore,
		Cache:     tenantConfigResolver,
		Providers: providerselect.NewStore(primaryPool),
		Endpoints: connectorIngressStore,
		Modules:   billingStore,
		Keys:      rowKeySet,
		Audit:     authAuditStore,
	})
	adminModulesHandler := adminmodules.NewHandler(adminmodules.Deps{
		Tenants:  tenantResolver,
		Auth:     authChecker,
		Registry: moduleRegistry,
		Settings: billingStore,
		Config:   tenantConfigStore,
		Cache:    cacheClient,
		Hub:      wsHub,
		Audit:    authAuditStore,
	})
	builtinRoutes["GET /admin/modules"] = http.HandlerFunc(adminModulesHandler.ServeList)
	builtinRoutes["GET /admin/modules/{name}"] = http.HandlerFunc(adminModulesHandler.ServeGet)
	builtinRoutes["PATCH /admin/modules/{name}/settings"] = http.HandlerFunc(adminModulesHandler.ServePatchSettings)
	builtinRoutes["GET /admin/connectors"] = http.HandlerFunc(adminConnectorsHandler.ServeList)
	builtinRoutes["GET /admin/connectors/{name}"] = http.HandlerFunc(adminConnectorsHandler.ServeGet)
	builtinRoutes["PATCH /admin/config"] = http.HandlerFunc(adminConnectorsHandler.ServePatchConfig)
	builtinRoutes["POST /admin/connectors/{name}/config/{key}/rotate"] = http.HandlerFunc(adminConnectorsHandler.ServeRotate)
	builtinRoutes["DELETE /admin/connectors/{name}/webhook"] = http.HandlerFunc(adminConnectorsHandler.ServeRevokeWebhook)
	moduleInstallWorker := &moduleinstall.Worker{
		Runtime:     runtime,
		PoolCfg:     poolCfg,
		Registry:    moduleRegistry,
		RolePerms:   rolePermissionMap,
		TenantStore: tenantStore,
		RoleStore:   roleStore,
		SyncPool:    syncPool,
		DiffEngine:  diffEngine,
		Storage:     storageBackend,
		Workers:     workflowWorkers,
		Hub:         wsHub,
	}

	river.AddWorker(jobWorkers, moduleInstallWorker)
	river.AddWorker(jobWorkers, &eventdelivery.Worker{ModuleRegistry: moduleRegistry, TenantStore: tenantStore, Pool: primaryPool})
	river.AddWorker(jobWorkers, &eventdelivery.EventsReplayWorker{ModuleRegistry: moduleRegistry, TenantStore: tenantStore, Pool: primaryPool})
	river.AddWorker(jobWorkers, &eventdelivery.SubscriberDeliveryWorker{ModuleRegistry: moduleRegistry, Invoker: eventInvoker})
	river.AddWorker(jobWorkers, &jobqueue.PartitionMaintenanceWorker{Pool: schemaPool, EventLedgerRetention: cfg.EventLedgerRetention})
	river.AddWorker(jobWorkers, &jobqueue.ReindexWorker{Pool: schemaPool})
	river.AddWorker(jobWorkers, &jobqueue.InviteExpiryWorker{TenantStore: tenantStore, InviteStore: inviteStore, AuditStore: authAuditStore})
	activityDue := &activityDueWorker{}
	river.AddWorker(jobWorkers, activityDue)
	commentNotify := &recordCommentNotifyWorker{}
	river.AddWorker(jobWorkers, commentNotify)
	commentRecipient := &recordCommentRecipientWorker{}
	river.AddWorker(jobWorkers, commentRecipient)
	river.AddWorker(jobWorkers, &jobqueue.DeviceTokenCleanupWorker{TenantStore: tenantStore, NotificationStore: notificationStore})
	unsubscribeCodec := notifications.NewUnsubscribeCodec(signingKeySet)
	river.AddWorker(jobWorkers, notify.NewEmailWorker(notify.EmailDeps{
		DB:                    primaryPool,
		Registry:              moduleRegistry,
		Config:                notificationConfig,
		Tenants:               tenantStore,
		Unsubscribe:           unsubscribeCodec,
		AppBaseURL:            cfg.AppBaseURL,
		PlatformDomain:        cfg.PlatformDomain,
		SMTPAllowPrivateHosts: cfg.NotificationSMTPAllowPrivateHosts,
	}))
	river.AddWorker(jobWorkers, &jobdispatch.Worker{
		ModuleRegistry: moduleRegistry,
		SchemaSyncPool: syncPool,
		Runtime:        runtime,
		TenantStore:    tenantStore,
		Roles:          roleStore,
		Deliveries:     &notify.ProviderDeliveries{DB: primaryPool, Tenants: tenantStore},
	})
	jobQueueClient, err := jobqueue.New(jobQueuePool, cfg, jobWorkers)
	if err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("create job queue client: %w", err)
	}

	// Enqueues background validation jobs for constraints Stage 4's schema
	// sync created NOT VALID — the job queue client didn't exist yet when
	// that ran, so it could only record the pending row (schema.Execute /
	// schema.EnqueuePendingValidations).
	if err := schema.EnqueuePendingValidations(ctx, primaryPool, jobQueueClient); err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("enqueue pending constraint validations: %w", err)
	}

	// provisionActivities, moduleInstallWorker, syncWorker, and
	// acceptResyncWorker were all constructed before jobQueueClient
	// existed (see their own RiverClient field doc comments) — wired now
	// that it does.
	provisionActivities.RiverClient = jobQueueClient
	moduleInstallWorker.RiverClient = jobQueueClient
	syncWorker.RiverClient = jobQueueClient
	acceptResyncWorker.RiverClient = jobQueueClient

	// Same "job queue client didn't exist yet" reasoning as
	// EnqueuePendingValidations just above, applied to data migrations:
	// Stage 4's schema sync (tenantsync.SyncAll) ran before this client
	// existed, so it could advance current_version but never enqueue a
	// migration job directly. This sweep catches every module × tenant
	// pair Stage 4 left with an un-advanced data_migration_version
	// watermark.
	if err := jobdispatch.EnqueueStartupDataMigrations(ctx, jobQueueClient, syncPool, tenantStore, orderedModules); err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("enqueue startup data migrations: %w", err)
	}

	adminapi.RegisterJobsRoutes(adminServer.Router(), adminapi.JobsDeps{
		Client: jobQueueClient,
		OutputDecryptor: func(kind string, output jsontext.Value) (jsontext.Value, error) {
			return tenantexport.DecryptOutput(rowKeySet, kind, output)
		},
	})

	adminapi.RegisterEventsRoutes(adminServer.Router(), adminapi.EventsDeps{
		ModuleRegistry: moduleRegistry,
		TenantStore:    tenantStore,
		Pool:           primaryPool,
		JobClient:      jobQueueClient,
	})

	adminapi.RegisterTenantRoutes(adminServer.Router(), adminapi.TenantDeps{
		Store:          tenantStore,
		SyncStatus:     syncPool,
		TableCounts:    syncPool,
		Membership:     roleStore,
		Users:          userStore,
		SessionRevoker: sessionRevoker,
		DomainCache:    cacheClient,
		Inviter:        inviteStore,
		Provisioner:    tenantprovision.NewProvisioner(temporalClient, systemworker.TaskQueue),
		Offboarder:     tenantoffboard.NewOffboarder(tenantStore, temporalClient, systemworker.TaskQueue, jobQueueClient, jobqueue.QueueAdmin),
		Exporter:       tenantexport.NewExporter(tenantStore, jobQueueClient, jobqueue.QueueAdmin),
		Importer:       tenantimport.NewImporter(tenantStore, jobQueueClient, jobqueue.QueueAdmin, rowKeySet),
		Storage:        storageBackend,
	})

	schemaAdmin := tenantsync.NewAdmin(tenantStore, moduleRegistry, syncPool, diffEngine, jobQueueClient, jobqueue.QueueAdmin)
	adminapi.RegisterSchemaRoutes(adminServer.Router(), adminapi.SchemaDeps{
		Status: schemaAdmin,
		Diff:   schemaAdmin,
		Sync:   schemaAdmin,
		Accept: schemaAdmin,
	})

	reloadLeader := &modulereload.Leader{
		Runtime:     runtime,
		PoolCfg:     poolCfg,
		Registry:    moduleRegistry,
		RolePerms:   rolePermissionMap,
		TenantStore: tenantStore,
		RoleStore:   roleStore,
		SyncPool:    syncPool,
		DiffEngine:  diffEngine,
		Storage:     storageBackend,
		Cache:       cacheClient,
		Workers:     workflowWorkers,
		RiverClient: jobQueueClient,
		Hub:         wsHub,
	}

	reloadFollower := &modulereload.Follower{
		Runtime:     runtime,
		PoolCfg:     poolCfg,
		Registry:    moduleRegistry,
		RolePerms:   rolePermissionMap,
		TenantStore: tenantStore,
		RoleStore:   roleStore,
		Storage:     storageBackend,
		Workers:     workflowWorkers,
		Hub:         wsHub,
	}

	instanceID := uuid.New().String()
	hotReloadCoordinator := hotreload.New(cacheClient, moduleRegistry, instanceID, hotreload.Config{
		ModuleDir: cfg.ModuleDir,
		LockTTL:   cfg.HotReloadLockTTL,
		// PollInterval/RegistryClient are left zero/nil: the registry-poll
		// trigger's own dependency (an external module registry service,
		// backlog goerp#563) doesn't exist yet — see
		// hotreload.RegistryClient's own doc comment.
	}, reloadLeader.Run, reloadFollower.Run)

	adminapi.RegisterModuleRoutes(adminServer.Router(), adminapi.ModulesDeps{
		Install: &moduleinstall.Installer{
			ModuleDir: cfg.ModuleDir,
			JobClient: jobQueueClient,
			JobQueue:  jobqueue.QueueAdmin,
		},
		Reload:        moduleReloadAdapter{coordinator: hotReloadCoordinator},
		ReloadEnabled: cfg.HotReloadEnabled,
	})

	adminapi.RegisterConfigRoutes(adminServer.Router(), adminapi.ConfigDeps{
		Tenants: tenantStore,
		Config:  tenantConfigStore,
	})
	adminapi.RegisterAccountRoutes(adminServer.Router(), adminapi.AccountDeps{
		DB:       primaryPool,
		Audit:    authAuditStore,
		Sessions: sessionRevoker,
		Jobs:     runtime.EventInsertClient(),
		Mailer:   inviteMailer,
	})

	notifier := notify.NewSender(notify.Deps{
		DB:        primaryPool,
		Store:     notificationStore,
		Registry:  moduleRegistry,
		Config:    notificationConfig,
		Providers: providerselect.NewStore(primaryPool),
		Tenants:   tenantStore,
		Members:   roleStore,
		Jobs:      runtime.EventInsertClient(),
		Hub:       wsHub,
	})
	runtime.SetNotifySender(notify.HostSender{Sender: notifier})

	e = &Engine{
		cfg:                    cfg,
		wasmRuntime:            runtime,
		syncPool:               syncPool,
		tenantStore:            tenantStore,
		sessionStore:           sessionStore,
		signingKeySet:          signingKeySet,
		tokenIssuer:            tokenIssuer,
		sessionRevoker:         sessionRevoker,
		authChecker:            authChecker,
		tenantResolver:         tenantResolver,
		moduleRegistry:         moduleRegistry,
		rolePermissionMap:      rolePermissionMap,
		jobQueue:               jobQueueClient,
		jobQueuePool:           jobQueuePool,
		secretsBackend:         secretsBackend,
		primaryDB:              primaryPool,
		replicaDB:              replicaPool,
		userStore:              userStore,
		recordSharesStore:      recordSharesStore,
		savedFiltersStore:      savedFiltersStore,
		recordActivityStore:    recordActivityStore,
		notificationStore:      notificationStore,
		notificationConfig:     notificationConfig,
		notifier:               notifier,
		txJobs:                 runtime.EventInsertClient(),
		unsubscribeCodec:       unsubscribeCodec,
		tenantLocales:          tenantLocales,
		scheduledActivityStore: scheduledActivityStore,
		activityTypeStore:      activityTypeStore,
		roleStore:              roleStore,
		filesStore:             filesStore,
		cacheClient:            cacheClient,
		searchClient:           searchClient,
		storageBackend:         storageBackend,
		temporalClient:         temporalClient,
		workflowWorkers:        workflowWorkers,
		systemWorker:           systemWorker,
		server:                 server,
		adminServer:            adminServer,
		wsHub:                  wsHub,
		tracer:                 tracer,
		tracerProvider:         tracerProvider,
		instanceID:             instanceID,
		hotReload:              hotReloadCoordinator,

		tenantConfigListener: tenantConfigListener,
		rolesListener:        rolesListener,
	}

	// Registered with River before e existed; River only starts working
	// jobs once the engine starts.
	activityDue.engine = e
	commentNotify.engine = e
	commentRecipient.engine = e

	// GET /_meta/permissions (goerp#417) is added here rather than to the
	// builtinRoutes literal above for the same reason dispatchORMRoute
	// couldn't be referenced there: dispatchPermissionsRoute is an
	// *Engine method, which doesn't exist until the literal above runs.
	builtinRoutes["GET /_meta/permissions"] = http.HandlerFunc(e.dispatchPermissionsRoute)

	builtinRoutes["POST /_meta/shares"] = http.HandlerFunc(e.dispatchSharesCreateRoute)
	builtinRoutes["GET /_meta/shares"] = http.HandlerFunc(e.dispatchSharesListRoute)
	builtinRoutes["DELETE /_meta/shares/{id}"] = http.HandlerFunc(e.dispatchSharesDeleteRoute)

	builtinRoutes["POST /_meta/saved-filters"] = http.HandlerFunc(e.dispatchSavedFiltersCreateRoute)
	builtinRoutes["GET /_meta/saved-filters"] = http.HandlerFunc(e.dispatchSavedFiltersListRoute)
	builtinRoutes["PATCH /_meta/saved-filters/{id}"] = http.HandlerFunc(e.dispatchSavedFiltersUpdateRoute)
	builtinRoutes["DELETE /_meta/saved-filters/{id}"] = http.HandlerFunc(e.dispatchSavedFiltersDeleteRoute)

	builtinRoutes["GET /_meta/activity"] = http.HandlerFunc(e.dispatchActivityListRoute)
	builtinRoutes["POST /_meta/activity"] = http.HandlerFunc(e.dispatchActivityCreateRoute)
	builtinRoutes["DELETE /_meta/activity/{id}"] = http.HandlerFunc(e.dispatchActivityDeleteRoute)
	builtinRoutes["GET /_meta/activity/followers"] = http.HandlerFunc(e.dispatchActivityFollowersListRoute)
	builtinRoutes["PUT /_meta/activity/followers"] = http.HandlerFunc(e.dispatchActivityFollowRoute)
	builtinRoutes["DELETE /_meta/activity/followers"] = http.HandlerFunc(e.dispatchActivityUnfollowRoute)

	builtinRoutes["GET /_meta/record-readers"] = http.HandlerFunc(e.dispatchRecordReadersRoute)

	builtinRoutes["GET /_meta/scheduled-activities"] = http.HandlerFunc(e.dispatchScheduledActivityListRoute)
	builtinRoutes["GET /_meta/scheduled-activities/mine"] = http.HandlerFunc(e.dispatchScheduledActivityMineRoute)
	builtinRoutes["POST /_meta/scheduled-activities"] = http.HandlerFunc(e.dispatchScheduledActivityCreateRoute)
	builtinRoutes["PATCH /_meta/scheduled-activities/{id}"] = http.HandlerFunc(e.dispatchScheduledActivityUpdateRoute)
	builtinRoutes["POST /_meta/scheduled-activities/{id}/done"] = http.HandlerFunc(e.dispatchScheduledActivityDoneRoute)
	builtinRoutes["DELETE /_meta/scheduled-activities/{id}"] = http.HandlerFunc(e.dispatchScheduledActivityCancelRoute)

	builtinRoutes["GET /_meta/activity-types"] = http.HandlerFunc(e.dispatchActivityTypeListRoute)
	builtinRoutes["GET /admin/activity-types"] = http.HandlerFunc(e.dispatchAdminActivityTypeListRoute)
	builtinRoutes["POST /admin/activity-types"] = http.HandlerFunc(e.dispatchAdminActivityTypeCreateRoute)
	builtinRoutes["PATCH /admin/activity-types/{key}"] = http.HandlerFunc(e.dispatchAdminActivityTypeUpdateRoute)
	builtinRoutes["PUT /admin/activity-types/order"] = http.HandlerFunc(e.dispatchAdminActivityTypeReorderRoute)
	builtinRoutes["DELETE /admin/activity-types/{key}"] = http.HandlerFunc(e.dispatchAdminActivityTypeDeleteRoute)

	builtinRoutes["GET /_notif/feed"] = http.HandlerFunc(e.dispatchNotifFeedRoute)
	builtinRoutes["GET /_notif/count"] = http.HandlerFunc(e.dispatchNotifCountRoute)
	builtinRoutes["POST /_notif/{id}/read"] = http.HandlerFunc(e.dispatchNotifReadRoute)
	builtinRoutes["POST /_notif/read-all"] = http.HandlerFunc(e.dispatchNotifReadAllRoute)
	builtinRoutes["DELETE /_notif/{id}"] = http.HandlerFunc(e.dispatchNotifDismissRoute)
	builtinRoutes["DELETE /_notif/all"] = http.HandlerFunc(e.dispatchNotifDismissAllRoute)
	builtinRoutes["POST /_notif/device-token"] = http.HandlerFunc(e.dispatchNotifDeviceTokenRoute)
	builtinRoutes["GET /_notif/preferences"] = http.HandlerFunc(e.dispatchNotifPreferencesRoute)
	builtinRoutes["PATCH /_notif/preferences"] = http.HandlerFunc(e.dispatchNotifPreferencesUpdateRoute)
	builtinRoutes["GET /_notif/unsubscribe"] = http.HandlerFunc(e.dispatchNotifUnsubscribeRoute)
	builtinRoutes["POST /_notif/unsubscribe"] = http.HandlerFunc(e.dispatchNotifUnsubscribeConfirmRoute)

	builtinRoutes["GET /_meta/schema"] = http.HandlerFunc(e.dispatchSchemaRoute)

	builtinRoutes["GET /modules/{module}/frontend/{file}"] = http.HandlerFunc(e.dispatchFrontendBundleRoute)
	builtinRoutes["GET /modules/{module}/translations/{file}"] = http.HandlerFunc(e.dispatchFrontendTranslationsRoute)

	builtinRoutes["GET /_ws"] = http.HandlerFunc(e.dispatchWSRoute)

	// Every builtin handler needs a registry entry before route resolution can dispatch it.
	if err := verifyBuiltinRouteParity(builtinRoutes, moduleRegistry.Snapshot().RouteTable()); err != nil {
		closeOnFailure()
		return nil, fmt.Errorf("verify builtin route parity: %w", err)
	}

	handler := buildChain(e, moduleRegistry, builtinRoutes, cfg.TrustedProxies, tenantResolver, authChecker, tracer, cacheClient, defaultRateLimit)
	if shell != nil {
		handler = shell.Wrap(handler)
	}

	server.SetHandler(handler)

	return e, nil
}

func (e *Engine) ModuleRegistry() *registry.ModuleRegistry {
	return e.moduleRegistry
}

func (e *Engine) JobQueue() *river.Client[pgx.Tx] {
	return e.jobQueue
}

func (e *Engine) Tracer() trace.Tracer {
	return e.tracer
}

// ServeErrors delivers an HTTP or admin server failure after Start
// returned; the caller stops the engine and exits non-zero on one.
func (e *Engine) ServeErrors() <-chan error {
	return e.serveErrs
}

// Start binds both listeners before starting anything else, so a taken
// port fails startup (engine-internals.md §2) instead of leaving an
// engine that reports ready while serving nothing.
func (e *Engine) Start(ctx context.Context) error {
	if err := e.server.Listen(); err != nil {
		return err
	}

	if err := e.adminServer.Listen(); err != nil {
		_ = e.server.Close()
		return err
	}

	e.serveErrs = make(chan error, 2)
	go func() {
		if err := e.server.Serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			e.serveErrs <- fmt.Errorf("http server: %w", err)
		}
	}()
	go func() {
		if err := e.adminServer.Serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			e.serveErrs <- fmt.Errorf("admin http server: %w", err)
		}
	}()

	if err := e.jobQueue.Start(ctx); err != nil {
		return fmt.Errorf("start job queue worker: %w", err)
	}

	if snap := e.moduleRegistry.Snapshot(); snap != nil {
		if err := e.workflowWorkers.SpawnAll(ctx, snap.Modules()); err != nil {
			return fmt.Errorf("spawn workflow workers: %w", err)
		}
	}

	if err := e.systemWorker.Start(ctx); err != nil {
		return fmt.Errorf("start system worker: %w", err)
	}

	if e.cfg.HotReloadEnabled {
		if err := e.hotReload.Start(ctx); err != nil {
			return fmt.Errorf("start hot reload coordinator: %w", err)
		}
	}

	e.tenantConfigListener.Start(ctx, nil)
	e.rolesListener.Start(ctx, nil)

	e.readiness.Store(true)

	return nil
}

func (e *Engine) Shutdown(ctx context.Context) error {
	log.Info().Msg("shutdown initiated")

	e.readiness.Store(false)

	if e.cfg.ShutdownDrainDelay > 0 {
		time.Sleep(e.cfg.ShutdownDrainDelay)
	}

	if e.cfg.HotReloadEnabled {
		e.hotReload.Stop()
	}

	e.tenantConfigListener.Stop()
	e.rolesListener.Stop()

	if err := e.adminServer.Shutdown(ctx); err != nil {
		log.Warn().Err(err).Msg("could not shut down admin server")
	}

	e.wsHub.Close(ctx)

	if err := e.jobQueue.Stop(ctx); err != nil {
		log.Warn().Err(err).Msg("could not stop job queue worker")
	}

	e.jobQueuePool.Close()

	e.workflowWorkers.StopAll(ctx)
	e.systemWorker.Stop()

	if err := e.wasmRuntime.Close(ctx); err != nil {
		log.Warn().Err(err).Msg("could not close wasm runtime")
	}

	if e.tracerProvider != nil {
		if err := e.tracerProvider.Shutdown(ctx); err != nil {
			log.Warn().Err(err).Msg("could not shut down OpenTelemetry tracer provider")
		}
	}

	return nil
}

func (e *Engine) newModuleContext(ctx context.Context, req EngineRequest, mod *module.LoadedModule) *wasm.ModuleContext {
	var fieldSecRegistry *fieldsec.FieldSecurityRegistry
	var eventRegistry *event.EventRegistry
	var computedIndex *computed.Index
	var computeTargets map[string]wasm.ComputeTarget
	var permRegistry *permission.PermissionRegistry
	var searchIndexRegistry *searchindex.Registry

	if e.moduleRegistry != nil {
		if snap := e.moduleRegistry.Snapshot(); snap != nil {
			fieldSecRegistry = snap.FieldSecRegistry()
			eventRegistry = snap.EventRegistry()
			computedIndex = snap.ComputedIndex()
			computeTargets = registry.ComputeTargets(snap)
			permRegistry = snap.PermissionRegistry()
			searchIndexRegistry = snap.SearchIndexRegistry()
		}
	}

	return wasm.NewModuleContext(req.ID, mod.Manifest.Name, req.UserID, req.ContactID, req.RolesLive, req.PermissionSet, req.TenantID, req.TenantSlug, req.TraceID, mod.Capabilities, e.wasmRuntime.TxLimiter(), wasm.ModuleSnapshot{
		ModelDecls:          mod.ModelDecls,
		FieldSecRegistry:    fieldSecRegistry,
		EventRegistry:       eventRegistry,
		ComputedIndex:       computedIndex,
		ComputeTargets:      computeTargets,
		PermissionRegistry:  permRegistry,
		SearchIndexRegistry: searchIndexRegistry,
		OwnedModels:         mod.Manifest.Schema.OwnedModels,
		ExtendsModels:       mod.Manifest.Schema.ExtendsModels,
		ConfigSchema:        mod.Manifest.ConfigSchema,
		UsesConfig:          mod.UsesConfig,
		JobTypes:            mod.Manifest.JobTypes,
		HTTPAllowlist:       mod.Manifest.HTTPAllowlist,
		ORMBulkMaxRows:      e.wasmRuntime.ORMBulkMaxRows(),
		ORMStatementTimeout: e.wasmRuntime.ORMStatementTimeout(),
	})
}

func (e *Engine) invokeHandler(
	ctx context.Context,
	inst *wasm.ModuleInstance,
	handlerName string,
	req EngineRequest,
	mod *module.LoadedModule,
) (EngineResponse, error) {
	moduleCtx := e.newModuleContext(ctx, req, mod)
	inst.SetModuleContext(moduleCtx)
	e.wasmRuntime.RegisterInstance(inst)
	defer func() {
		e.wasmRuntime.UnregisterInstance(inst)
		moduleCtx.RollbackAll()
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

	var wire abiv1.Response
	if err := msgpack.Unmarshal(respBytes, &wire); err != nil {
		return EngineResponse{}, fmt.Errorf("unmarshal response: %w", err)
	}

	bodyBytes, err := handlerResponseBody(wire)
	if err != nil {
		return EngineResponse{}, err
	}

	return EngineResponse{StatusCode: wire.StatusCode, Headers: wire.Headers, Body: bodyBytes}, nil
}

// handlerResponseBody validates a handler response's JSON body and escapes
// it as v1's Encoder defaults do, since it reaches the client verbatim via
// writeResponse's w.Write.
func handlerResponseBody(wire abiv1.Response) ([]byte, error) {
	if len(wire.Body) == 0 {
		return nil, nil
	}

	body := jsontext.Value(wire.Body)
	if err := body.Format(jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true)); err != nil {
		return nil, fmt.Errorf("validate response body: %w", err)
	}

	return body, nil
}
