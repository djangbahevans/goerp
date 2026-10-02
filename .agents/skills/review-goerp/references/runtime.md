# Runtime wiring and health

Use `internal/engine/engine.go`, `internal/engine/httpx/health.go`, their tests and `../nexus-docs/docs/engine-internals.md` sections on startup and health as evidence. Recheck changed paths rather than assuming this list exhausts current dependencies.

## Startup and optional dependencies

- Primary Postgres and Redis connection failures abort construction. Schema-sync Postgres is initialized separately and its failure also returns an error.
- Replica Postgres, configured Meilisearch and object storage connection failures log warnings and permit construction to continue. Meilisearch construction is skipped when its URL is unset.
- Temporal connection failure also logs a warning in current wiring; inspect its consumers for nil safety. The startup document's explicit warn-only list does not exhaustively describe that wiring. Surface relevant doc/code differences rather than classifying new dependencies by omission alone.
- Trace values returned by failed constructors; dependencies can be nil after successful engine construction. Check affected consumers, including health checks, HTTP handlers and background workers, for guards or an explicit availability contract. A constructor-level guard does not protect later calls.

`TestHealthEndpointDefaultConfigDoesNotPanic` in `internal/engine/engine_test.go` exercises health with default wiring. Extend relevant regression coverage when dependency behavior changes, following root implementation instructions for code edits.

## Health and readiness

- `/_health` writes HTTP 503 only when the `postgres_primary` check is present and non-ok. Other failing checks produce a degraded report with HTTP 200.
- Nil optional dependencies skip their probes and currently report ok. Probe latency is measured and rounded to milliseconds; zero latency is common, not a required invariant.
- `/_ready` reports module counts and failures separately from readiness. A failed non-critical module does not by itself make readiness false.
- Current engine readiness checks shutdown state and pings primary Postgres. Readiness can therefore become 503 after startup, during shutdown or when that ping fails. The document's startup-focused description does not capture all current runtime conditions; review against both intended requirements and callback behavior.
- Confirm response shapes and statuses in endpoint tests when changing handlers or wiring. Do not conflate health, completed startup, module load state and current request-serving readiness.
