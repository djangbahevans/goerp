# GoERP

GoERP is a modular, multi-tenant ERP platform: a Go engine that hosts WASM-compiled business modules behind a stable host ABI, a React shell that dynamically loads each module's verified frontend bundle, and a `goerp` CLI for module lifecycle, schema, and operational management.

Implementation work is tracked as GitHub issues.

## Repository structure

```text
goerp/
├── cmd/
│   ├── engine/            # the engine binary — single process, hosts WASM modules, runs the HTTP server
│   ├── goerp/             # CLI entrypoint (module build/test/install are subcommands of this)
│   └── workflow-worker/   # standalone process for modules with workflow declarations
├── internal/
│   ├── engine/          # core subsystems: manifest, schema sync, host ABI, ...
│   ├── contracttest/     # full-engine test harness (real Postgres/Redis)
│   └── cli/              # command implementations backing cmd/goerp
├── sdk/                  # public Module SDK (Go + React + supporting packages)
├── shell/                # React 19 SPA that hosts and renders module frontends
├── modules/               # example/first-party .erp modules used for e2e testing
├── scripts/               # operational scripts (not part of any build/test pipeline)
└── compose.dev.yml        # local development dependency stack
```

## Local development environment

The full dependency stack for local development runs via Docker Compose. The `goerp` engine binary itself is **not** containerized — run it locally on the host and it connects to the services below over `localhost`.

### Start the stack

```bash
docker compose -f compose.dev.yml up -d
```

Stop it with:

```bash
docker compose -f compose.dev.yml down
```

Data is persisted in named volumes (`postgres_data`, `meili_data`, `seaweedfs_data`, `temporal_data`), so state survives restarts. To wipe everything and start clean:

```bash
docker compose -f compose.dev.yml down -v
```

### Services

| Service | Host address | Purpose | Credentials / notes |
| --- | --- | --- | --- |
| Postgres | `localhost:55432` | Primary database | Superuser `goerp`, password `dev`. Database `goerp_dev` is the running engine's; database `goerp` is the test suite's. Roles `engine_user` and `schema_sync_user`, password `dev`. Bound to a static non-default port to avoid clashing with a locally installed Postgres. |
| PgBouncer | `localhost:6432` | Connection pooler in front of Postgres (transaction pooling) | Serves `engine_user` on `goerp_dev`, the engine's primary pool, and `goerp` on `goerp` for the test suite. |
| Redis | `localhost:6379` | Cache / pub-sub | No auth |
| RedisInsight | [http://localhost:8001](http://localhost:8001) | Redis GUI | Connect it to `redis:6379` inside the compose network |
| Meilisearch | [http://localhost:7700](http://localhost:7700) | Search index | Master key: `2f14b775804ecaf5dc4084d32aa034a7` |
| SeaweedFS | `localhost:8333` (filer/UI), `localhost:8334` (S3 API) | Object storage | S3-compatible API on 8334 |
| Jaeger | [http://localhost:16686](http://localhost:16686) (UI) | Distributed tracing | OTLP ingest on `4317` (gRPC) / `4318` (HTTP) |
| Mailpit | [http://localhost:8025](http://localhost:8025) (UI), `localhost:1025` (SMTP) | Catches outgoing dev email | No auth |
| Temporal | `localhost:7233` (gRPC), [http://localhost:8233](http://localhost:8233) (UI) | Workflow engine | Runs in `start-dev` mode with its own embedded SQLite database |

### Connecting the engine

Point the locally-run engine binary at PgBouncer (not Postgres directly) and the other services above using their `localhost` ports. All services expose healthchecks where startup ordering matters (PgBouncer waits on Postgres being healthy).

The engine logs in as two Postgres roles (data-layer.md §2.2): `engine_user` for its primary and job-queue pools, through PgBouncer, and `schema_sync_user` for schema sync, provisioning and startup bootstrap, directly. `schema_sync_user` owns the `system` schema and every tenant table; `engine_user` has DML on them and no DDL. `docker/postgres-initdb/` creates the roles, the `goerp_dev` database and the tenant-role functions when the `postgres_data` volume is first initialized, so a volume created before those scripts existed needs `docker compose -f compose.dev.yml down -v` to pick them up. A production cluster runs the same two scripts once at cluster setup: `01-roles.sql` as the superuser, without its dev-only `CREATE DATABASE goerp_dev`, then `database/setup.sql` in the engine's database, with real passwords.

### Running the engine

With the stack up, the only environment variables actually required are `GOERP_DB_PRIMARY_DSN`, `GOERP_DB_SCHEMA_SYNC_DSN` and `GOERP_ADMIN_TOKEN`, the superadmin bearer token the admin API on `127.0.0.1:8081` checks, which the engine refuses to start without — everything else defaults to matching the services above (`GOERP_REDIS_ADDR` already defaults to `localhost:6379`, `GOERP_LISTEN_ADDR` to `:8080`). `GOERP_STORAGE_LOCAL_DIR` isn't required either, but without it the local storage backend fails to construct (a startup warning, not a fatal error — object storage checks then read back as unconfigured rather than actually working).

```bash
export GOERP_DB_PRIMARY_DSN="postgres://engine_user:dev@localhost:6432/goerp_dev"
export GOERP_DB_SCHEMA_SYNC_DSN="postgres://schema_sync_user:dev@localhost:55432/goerp_dev"
export GOERP_ADMIN_TOKEN="dev-admin-token"
export GOERP_STORAGE_LOCAL_DIR="./storage"

go run ./cmd/engine
```

Confirm it's up:

```bash
curl localhost:8080/_health
```

A healthy response looks like:

```json
{
  "status": "healthy",
  "version": "dev",
  "uptime_seconds": 12,
  "checks": {
    "postgres_primary": { "status": "ok", "latency_ms": 1.1 },
    "postgres_replica": { "status": "ok", "latency_ms": 0 },
    "redis": { "status": "ok", "latency_ms": 0.6 },
    "meilisearch": { "status": "ok", "latency_ms": 0 },
    "object_storage": { "status": "ok", "latency_ms": 0.2 }
  }
}
```

`postgres_replica`/`meilisearch` read `"ok"` with `0` latency when they're simply unconfigured (`GOERP_DB_REPLICA_DSN`/`GOERP_MEILISEARCH_URL` unset) — not because they were actually checked. `GET localhost:8080/_ready` reports whether the full startup sequence has completed and is what a Kubernetes readiness probe would check.

## Operational scripts

`scripts/generate-ghana-pmtiles.sh` extracts a Ghana-region [PMTiles](https://docs.protomaps.com/) basemap archive from Protomaps' daily basemap build and uploads it to object storage (SeaweedFS by default, matching the stack above) — the file `LocationField`'s `tileUrl` points at (`docs/components/location-field.md` in nexus-docs). Requires the `go-pmtiles` CLI (`go install github.com/protomaps/go-pmtiles@latest`) and the AWS CLI. Re-run it whenever the archive needs refreshing; it isn't part of any build or CI pipeline.

## Frontend build-time config

The shell app (`shell/packages/app`) takes deployment-wide build-time config via Vite env vars — copy `shell/packages/app/.env.example` to `.env` and fill in for a real deployment. Currently just `VITE_MAP_TILE_URL` (the PMTiles archive URL above); unset, `LocationField` renders its plain-background fallback with no other behavior change.
