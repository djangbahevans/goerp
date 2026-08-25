-- Extensions enabled once per database on container init, not per-tenant
-- schema sync — go-sdk-reference.md §22 "Tree", "Bootstrap note": ltree
-- is a Postgres extension enabled once per database, not something
-- per-tenant schema sync does. In a real deployment this runs once
-- against the shared Postgres cluster as a manual step; here it runs
-- automatically via Postgres's own docker-entrypoint-initdb.d convention
-- so a fresh dev/CI database has it without a manual step.
CREATE EXTENSION IF NOT EXISTS ltree;
