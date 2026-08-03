// Package engine is the composition root: Engine.New runs Stage 1
// (engine-internals.md §2) — secrets backend, primary/replica Postgres,
// Redis, Meilisearch (optional), object storage — and wires the results
// into the HTTP server's injected health/ready checks. Primary Postgres and
// Redis failures are fail-hard (New returns an error); replica Postgres,
// Meilisearch, and object storage failures only warn and continue, per the
// explicit warn-only list in engine-internals.md §2. Stage 2+ (module
// loading, schema sync, instance pooling) is out of scope here — see #34's
// notes for why, and #13/#19 for where that lands.
package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/httpx"
	"github.com/djangbahevans/goerp/internal/engine/search"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type Engine struct {
	cfg            *config.Config
	secretsBackend secrets.Backend
	primaryDB      *pgxpool.Pool
	replicaDB      *pgxpool.Pool
	cacheClient    *cache.Client
	searchClient   *search.Client
	storageBackend storage.Backend
	server         *httpx.Server
	readiness      atomic.Bool
}

func New(cfg *config.Config) (*Engine, error) {
	ctx := context.Background()

	secretsBackend, err := secrets.New(cfg.SecretsBackend)
	if err != nil {
		return nil, fmt.Errorf("create secrets backend: %w", err)
	}

	primaryPool, err := db.New(ctx, cfg.DBPrimaryDSN)
	if err != nil {
		return nil, fmt.Errorf("connect to primary database: %w", err)
	}

	var replicaPool *pgxpool.Pool
	if cfg.DBReplicaDSN != "" {
		replicaPool, err = db.New(ctx, cfg.DBReplicaDSN)
		if err != nil {
			log.Warn().Err(err).Msg("could not connect to replica db")
		}
	}

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

		return primaryPool.Ping(ctx)
	}
	server := httpx.NewServer(&httpx.Config{ListenAddr: cfg.ListenAddr}, readyFn)

	startedAt := time.Now()
	server.SetHealthFn(func(ctx context.Context) httpx.HealthReport {
		checks := make(map[string]httpx.CheckResult)

		checks["postgres_primary"] = httpx.ProbeCheck(ctx, func(ctx context.Context) error {
			return primaryPool.Ping(ctx)
		})
		checks["postgres_replica"] = httpx.ProbeCheck(ctx, func(ctx context.Context) error {
			if replicaPool == nil {
				return nil
			}
			return replicaPool.Ping(ctx)
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

	e = &Engine{
		cfg:            cfg,
		secretsBackend: secretsBackend,
		primaryDB:      primaryPool,
		replicaDB:      replicaPool,
		cacheClient:    cacheClient,
		searchClient:   searchClient,
		storageBackend: storageBackend,
		server:         server,
	}

	return e, nil
}

func (e *Engine) Start(ctx context.Context) error {

	go func() {
		if err := e.server.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("http server error")
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

	return nil
}
