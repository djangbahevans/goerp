-- Extensions enabled once per database on container init, not per-tenant
-- schema sync — go-sdk-reference.md §22 "Tree", "Bootstrap note": ltree
-- is a Postgres extension enabled once per database, not something
-- per-tenant schema sync does. In a real deployment this runs once
-- against the shared Postgres cluster as a manual step; here it runs
-- automatically via Postgres's own docker-entrypoint-initdb.d convention
-- so a fresh dev/CI database has it without a manual step.
CREATE EXTENSION IF NOT EXISTS ltree;

-- pg_partman (goerp#194, data-layer.md §2.6) manages the monthly range
-- partitions on event_log/audit_log — partman.create_parent registers
-- each table at tenant provisioning, and a platform-wide River periodic
-- job calls partman.run_maintenance() to keep partitions ahead of need
-- (no pg_partman_bgw background worker, so no shared_preload_libraries
-- change is needed here). Conventionally installed into its own schema
-- rather than public.
CREATE SCHEMA IF NOT EXISTS partman;
CREATE EXTENSION IF NOT EXISTS pg_partman SCHEMA partman;
