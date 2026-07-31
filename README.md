# GoERP

GoERP is a modular, multi-tenant ERP platform: a Go engine that hosts WASM-compiled business modules behind a stable host ABI, a React shell that dynamically loads each module's verified frontend bundle, and a `goerp` CLI for module lifecycle, schema, and operational management.

Implementation work is tracked as GitHub issues.

## Repository structure

```text
goerp/
├── cmd/
│   ├── goerp/          # CLI entrypoint
│   └── module/         # module build entrypoint
├── internal/
│   ├── engine/          # core subsystems: manifest, schema sync, host ABI, ...
│   ├── contracttest/     # full-engine test harness (real Postgres/Redis)
│   └── cli/              # command implementations backing cmd/goerp
├── sdk/                  # public Module SDK (Go + React + supporting packages)
├── shell/                # React 19 SPA that hosts and renders module frontends
├── modules/               # example/first-party .erp modules used for e2e testing
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
| Postgres | `localhost:55432` | Primary database | user `goerp`, password `dev`, db `goerp`. Bound to a static non-default port to avoid clashing with a locally installed Postgres. |
| PgBouncer | `localhost:6432` | Connection pooler in front of Postgres (transaction pooling) | Same credentials as Postgres. Prefer this over connecting to Postgres directly. |
| Redis | `localhost:6379` | Cache / pub-sub | No auth |
| RedisInsight | [http://localhost:8001](http://localhost:8001) | Redis GUI | Connect it to `redis:6379` inside the compose network |
| Meilisearch | [http://localhost:7700](http://localhost:7700) | Search index | Master key: `2f14b775804ecaf5dc4084d32aa034a7` |
| SeaweedFS | `localhost:8333` (filer/UI), `localhost:8334` (S3 API) | Object storage | S3-compatible API on 8334 |
| Jaeger | [http://localhost:16686](http://localhost:16686) (UI) | Distributed tracing | OTLP ingest on `4317` (gRPC) / `4318` (HTTP) |
| Mailpit | [http://localhost:8025](http://localhost:8025) (UI), `localhost:1025` (SMTP) | Catches outgoing dev email | No auth |
| Temporal | `localhost:7233` (gRPC), [http://localhost:8233](http://localhost:8233) (UI) | Workflow engine | Runs in `start-dev` mode with its own embedded SQLite database |

### Connecting the engine

Point the locally-run engine binary at PgBouncer (not Postgres directly) and the other services above using their `localhost` ports. All services expose healthchecks where startup ordering matters (PgBouncer waits on Postgres being healthy).
