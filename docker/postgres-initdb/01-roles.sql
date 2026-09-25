-- Cluster-wide roles (data-layer.md §2.2). Postgres's docker entrypoint
-- runs this once, as the superuser, on an empty data directory; a
-- production cluster runs the same statements once at cluster setup, with
-- real passwords.
--
-- engine_user: every query the engine builds itself, through PgBouncer.
-- schema_sync_user: schema sync, provisioning, the system-schema bootstrap
--   and River's migrations; owns system and every tenant table.
-- tenant_role_admin: owns system.create_tenant_role/drop_tenant_role
--   (database/setup.sql), so schema_sync_user itself stays NOCREATEROLE.
CREATE ROLE engine_user WITH LOGIN PASSWORD 'dev' NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE schema_sync_user WITH LOGIN PASSWORD 'dev' NOSUPERUSER NOCREATEDB NOCREATEROLE BYPASSRLS;
CREATE ROLE tenant_role_admin WITH NOLOGIN NOSUPERUSER NOCREATEDB CREATEROLE;

-- The locally run engine uses goerp_dev; the test suite connects to goerp
-- as the superuser. Separate databases keep tables the tests create as the
-- superuser from being the ones the engine's roles have to own.
CREATE DATABASE goerp_dev;
