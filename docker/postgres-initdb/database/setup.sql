-- Extensions enabled once per database on container init, not per-tenant
-- schema sync — go-sdk-reference.md §22 "Tree", "Bootstrap note": ltree
-- is a Postgres extension enabled once per database, not something
-- per-tenant schema sync does. In a real deployment this runs once
-- against the shared Postgres cluster as a manual step; here it runs
-- automatically via Postgres's own docker-entrypoint-initdb.d convention
-- so a fresh dev/CI database has it without a manual step.
CREATE EXTENSION IF NOT EXISTS ltree;

-- pg_trgm backs host.search.query's trigram similarity search
-- (data-layer.md §5.4) — the initial-build host.search.query backend,
-- Postgres trigram similarity, needs the similarity()/% operators this
-- extension provides.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- pg_partman (goerp#194, data-layer.md §2.6) manages the monthly range
-- partitions on event_log/audit_log — partman.create_parent registers
-- each table at tenant provisioning, and a platform-wide River periodic
-- job calls partman.run_maintenance() to keep partitions ahead of need
-- (no pg_partman_bgw background worker, so no shared_preload_libraries
-- change is needed here). Conventionally installed into its own schema
-- rather than public.
CREATE SCHEMA IF NOT EXISTS partman;
CREATE EXTENSION IF NOT EXISTS pg_partman SCHEMA partman;

-- schema_sync_user registers tenant tables with create_parent at
-- provisioning and runs run_maintenance(), which creates partitions and
-- writes partman's own config tables.
GRANT ALL ON SCHEMA partman TO schema_sync_user;
GRANT ALL ON ALL TABLES IN SCHEMA partman TO schema_sync_user;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA partman TO schema_sync_user;
GRANT EXECUTE ON ALL PROCEDURES IN SCHEMA partman TO schema_sync_user;

-- Roles and privileges (data-layer.md §2.2). schema_sync_user owns system,
-- creates tenant schemas, and creates every table in both; engine_user gets
-- DML on what it creates in system. Tenant schemas get the same default
-- privileges at provisioning.
GRANT CREATE ON DATABASE :"DBNAME" TO schema_sync_user;
CREATE SCHEMA system AUTHORIZATION schema_sync_user;
GRANT USAGE ON SCHEMA system TO engine_user;
ALTER DEFAULT PRIVILEGES FOR ROLE schema_sync_user IN SCHEMA system
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO engine_user;
ALTER DEFAULT PRIVILEGES FOR ROLE schema_sync_user IN SCHEMA system
    GRANT USAGE, SELECT ON SEQUENCES TO engine_user;

-- Tenant roles are created and dropped only through these two functions.
-- A tenant's role has the same name as its schema, so the slug check
-- mirrors system.tenants' own, plus Postgres's 63-byte identifier limit,
-- past which the name would be silently truncated.
CREATE FUNCTION system.create_tenant_role(slug text) RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = system, pg_temp
AS $$
DECLARE
    role_name text := 'tenant_' || slug;
BEGIN
    IF slug !~ '^[a-z][a-z0-9\-]{1,62}[a-z0-9]$' OR octet_length(role_name) > 63 THEN
        RAISE EXCEPTION 'invalid tenant slug: %', slug USING ERRCODE = 'invalid_parameter_value';
    END IF;
    BEGIN
        EXECUTE format('CREATE ROLE %I NOLOGIN', role_name);
    EXCEPTION WHEN duplicate_object THEN
        NULL;
    END;
    EXECUTE format('GRANT %I TO engine_user WITH INHERIT FALSE, SET TRUE', role_name);
END;
$$;

CREATE FUNCTION system.drop_tenant_role(slug text) RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = system, pg_temp
AS $$
BEGIN
    IF slug !~ '^[a-z][a-z0-9\-]{1,62}[a-z0-9]$' THEN
        RAISE EXCEPTION 'invalid tenant slug: %', slug USING ERRCODE = 'invalid_parameter_value';
    END IF;
    EXECUTE format('DROP ROLE IF EXISTS %I', 'tenant_' || slug);
END;
$$;

ALTER FUNCTION system.create_tenant_role(text) OWNER TO tenant_role_admin;
ALTER FUNCTION system.drop_tenant_role(text) OWNER TO tenant_role_admin;
REVOKE ALL ON FUNCTION system.create_tenant_role(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION system.drop_tenant_role(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION system.create_tenant_role(text) TO schema_sync_user;
GRANT EXECUTE ON FUNCTION system.drop_tenant_role(text) TO schema_sync_user;
