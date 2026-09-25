package tenantprovision

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5/pgconn"
)

// The roles docker/postgres-initdb creates (data-layer.md §2.2).
const (
	schemaSyncRoleDSN = "postgres://schema_sync_user:dev@localhost:55432/goerp"
	engineRoleDSN     = "postgres://engine_user:dev@localhost:55432/goerp"
)

// openRolePools skips when the dev Postgres isn't running at all, and fails
// when it is but lacks the roles: a volume initialized before the roles
// existed needs recreating.
func openRolePools(t *testing.T) (schemaSync, engine *sql.DB) {
	t.Helper()
	superuser, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	_ = superuser.Close()

	schemaSync, err = db.New(schemaSyncRoleDSN)
	if err != nil {
		t.Fatalf("connect as schema_sync_user (recreate the dev stack with docker compose down -v): %v", err)
	}
	t.Cleanup(func() { _ = schemaSync.Close() })
	engine, err = db.New(engineRoleDSN)
	if err != nil {
		t.Fatalf("connect as engine_user (recreate the dev stack with docker compose down -v): %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return schemaSync, engine
}

func provisionAsSchemaSync(t *testing.T, schemaSync *sql.DB) string {
	t.Helper()
	ctx := t.Context()
	slug := uniqueSlug(t)
	a := &Activities{schemaSyncPool: schemaSync}
	t.Cleanup(func() {
		_, _ = schemaSync.Exec(`DELETE FROM partman.part_config WHERE parent_table LIKE $1`, "tenant_"+slug+".%")
		_, _ = schemaSync.Exec(`DROP SCHEMA IF EXISTS ` + tenantschema.Name(slug) + ` CASCADE`)
	})
	if err := a.CreateTenantSchema(ctx, slug); err != nil {
		t.Fatalf("CreateTenantSchema: %v", err)
	}
	if err := a.CreateEngineTables(ctx, slug); err != nil {
		t.Fatalf("CreateEngineTables: %v", err)
	}
	return slug
}

func requirePermissionDenied(t *testing.T, err error, what string) {
	t.Helper()
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "42501" {
		t.Fatalf("%s: got %v, want permission denied (42501)", what, err)
	}
}

func auditLogPartitions(t *testing.T, conn *sql.DB, slug string) []string {
	t.Helper()
	rows, err := conn.Query(`
		SELECT format('%I.%I', n.nspname, c.relname)
		FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND p.relname = 'audit_log' AND c.relkind = 'r'
		ORDER BY 1`, "tenant_"+slug)
	if err != nil {
		t.Fatalf("list audit_log partitions: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan partition: %v", err)
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		t.Fatal("audit_log has no partitions")
	}
	return names
}

func TestProvisioning_EngineRoleHasDMLButNoDDLOnTenantTables(t *testing.T) {
	schemaSync, engine := openRolePools(t)
	slug := provisionAsSchemaSync(t, schemaSync)
	schema := tenantschema.Name(slug)

	if _, err := engine.Exec(`INSERT INTO ` + schema + `.module_config (module_name, key, value, value_type) VALUES ('m', 'm.k', '1', 'number')`); err != nil {
		t.Fatalf("engine_user insert into module_config: %v", err)
	}
	if _, err := engine.Exec(`UPDATE ` + schema + `.module_config SET value = '2'`); err != nil {
		t.Fatalf("engine_user update module_config: %v", err)
	}
	if _, err := engine.Exec(`DELETE FROM ` + schema + `.module_config`); err != nil {
		t.Fatalf("engine_user delete from module_config: %v", err)
	}

	_, err := engine.Exec(`CREATE TABLE ` + schema + `.engine_ddl (id int)`)
	requirePermissionDenied(t, err, "engine_user CREATE TABLE in the tenant schema")
	_, err = engine.Exec(`ALTER TABLE ` + schema + `.module_config ADD COLUMN extra int`)
	requirePermissionDenied(t, err, "engine_user ALTER TABLE on module_config")
}

func TestProvisioning_AuditLogIsAppendOnlyForEngineRole(t *testing.T) {
	schemaSync, engine := openRolePools(t)
	slug := provisionAsSchemaSync(t, schemaSync)
	schema := tenantschema.Name(slug)

	if _, err := engine.Exec(`INSERT INTO ` + schema + `.audit_log (table_name, record_id, operation) VALUES ('t', uuidv7(), 'INSERT')`); err != nil {
		t.Fatalf("engine_user insert into audit_log: %v", err)
	}
	_, err := engine.Exec(`UPDATE ` + schema + `.audit_log SET table_name = 'x'`)
	requirePermissionDenied(t, err, "engine_user UPDATE audit_log")
	_, err = engine.Exec(`DELETE FROM ` + schema + `.audit_log`)
	requirePermissionDenied(t, err, "engine_user DELETE FROM audit_log")

	for _, p := range auditLogPartitions(t, schemaSync, slug) {
		_, err := engine.Exec(`UPDATE ` + p + ` SET table_name = 'x'`)
		requirePermissionDenied(t, err, "engine_user UPDATE "+p)
		_, err = engine.Exec(`DELETE FROM ` + p)
		requirePermissionDenied(t, err, "engine_user DELETE FROM "+p)
	}
}

// A partition pg_partman creates after provisioning gets the schema's
// default privileges, UPDATE and DELETE included, until partition
// maintenance revokes them.
func TestPartitionMaintenance_RevokesMutationsOnNewAuditLogPartitions(t *testing.T) {
	schemaSync, engine := openRolePools(t)
	slug := provisionAsSchemaSync(t, schemaSync)
	parent := "tenant_" + slug + ".audit_log"
	before := auditLogPartitions(t, schemaSync, slug)

	if _, err := schemaSync.Exec(`UPDATE partman.part_config SET premake = premake + 2, infinite_time_partitions = true WHERE parent_table = $1`, parent); err != nil {
		t.Fatalf("raise premake: %v", err)
	}
	if _, err := schemaSync.Exec(`SELECT partman.run_maintenance($1)`, parent); err != nil {
		t.Fatalf("run_maintenance: %v", err)
	}
	after := auditLogPartitions(t, schemaSync, slug)
	if len(after) <= len(before) {
		t.Fatalf("run_maintenance created no partitions: %d before, %d after", len(before), len(after))
	}
	newest := after[len(after)-1]
	if _, err := engine.Exec(`UPDATE ` + newest + ` SET table_name = 'x'`); err != nil {
		t.Fatalf("precondition: expected engine_user to be able to UPDATE %s before the revoke, got %v", newest, err)
	}

	if err := enginetables.RevokeAppendOnlyMutations(t.Context(), schemaSync, ""); err != nil {
		t.Fatalf("RevokeAppendOnlyMutations: %v", err)
	}
	for _, p := range after {
		_, err := engine.Exec(`UPDATE ` + p + ` SET table_name = 'x'`)
		requirePermissionDenied(t, err, "engine_user UPDATE "+p)
	}
}
