package tenantprovision

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantsync "github.com/djangbahevans/goerp/internal/engine/tenant/sync"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5/pgconn"
	"go.temporal.io/sdk/temporal"
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
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), schemaSync, slug) })
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
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit_log partitions: %v", err)
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

func TestProvisioning_TenantRoleCanUseOnlyItsOwnSchema(t *testing.T) {
	schemaSync, engine := openRolePools(t)
	slug := provisionAsSchemaSync(t, schemaSync)
	other := provisionAsSchemaSync(t, schemaSync)
	role := "tenant_" + slug

	for schema, want := range map[string]bool{role: true, "system": false, "tenant_" + other: false, "partman": false} {
		var got bool
		if err := engine.QueryRow(`SELECT has_schema_privilege($1, $2, 'USAGE')`, role, schema).Scan(&got); err != nil {
			t.Fatalf("has_schema_privilege(%s, %s): %v", role, schema, err)
		}
		if got != want {
			t.Errorf("%s USAGE on %s = %v, want %v", role, schema, got, want)
		}
	}

	tx, err := engine.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	var current string
	if err := tx.QueryRow(`SELECT set_config('role', $1, true), current_user`, role).Scan(new(string), &current); err != nil {
		t.Fatalf("engine_user SET ROLE %s: %v", role, err)
	}
	if current != role {
		t.Errorf("current_user = %q, want %q", current, role)
	}
}

// Schema sync runs as the superuser here: in the test database the system
// tables it records progress in belong to the superuser, not
// schema_sync_user.
func TestSchemaSync_GrantsTenantRoleDMLOnModuleTablesOnly(t *testing.T) {
	schemaSync, engine := openRolePools(t)
	slug := provisionAsSchemaSync(t, schemaSync)
	ctx := t.Context()

	superuser, err := db.New(localPostgresDSN)
	if err != nil {
		t.Fatalf("connect as superuser: %v", err)
	}
	t.Cleanup(func() { _ = superuser.Close() })
	syncPool := schema.NewPool(superuser, 5*time.Second)
	if err := syncPool.Bootstrap(ctx); err != nil {
		t.Fatalf("schema pool Bootstrap: %v", err)
	}
	mod := &module.LoadedModule{
		Status: module.StatusReady,
		Manifest: manifest.Manifest{
			Name:    "widgets",
			Version: "1.0.0",
			Schema:  manifest.SchemaConfig{OwnedModels: []string{"sales.widget"}},
		},
		ModelDecls: []model.ModelDeclaration{widgetModel()},
	}
	tn := tenant.Tenant{ID: uuid.New().String(), Slug: slug}
	t.Cleanup(func() {
		_, _ = superuser.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1`, tn.ID)
	})
	if err := tenantsync.SyncOne(ctx, syncPool, schema.NewSchemaDiffEngine(&schema.Config{}), tn, mod, nil); err != nil {
		t.Fatalf("SyncOne: %v", err)
	}

	// A sequence-backed column, which schema sync doesn't create itself,
	// picked up by the grant step on the next sync.
	name := tenantschema.Name(slug)
	if _, err := superuser.Exec(`ALTER TABLE ` + name + `.widgets ADD COLUMN seq bigserial`); err != nil {
		t.Fatalf("add bigserial column: %v", err)
	}
	sess, err := syncPool.BeginSync(ctx, tn.ID, slug, mod.Manifest.Name, &mod.Manifest)
	if err != nil {
		t.Fatalf("BeginSync: %v", err)
	}
	if err := schema.NewSchemaDiffEngine(&schema.Config{}).SyncTenantRoleGrants(ctx, sess, mod.ModelDecls); err != nil {
		t.Fatalf("SyncTenantRoleGrants: %v", err)
	}
	if err := sess.Close(ctx); err != nil {
		t.Fatalf("close sync session: %v", err)
	}

	asTenant := func(stmt string) error {
		tx, err := engine.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`SELECT set_config('role', $1, true)`, "tenant_"+slug); err != nil {
			return err
		}
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
		return tx.Commit()
	}

	for _, stmt := range []string{
		`INSERT INTO ` + name + `.widgets (name, tenant_id) VALUES ('w', uuidv7())`,
		`SELECT * FROM ` + name + `.widgets`,
		`UPDATE ` + name + `.widgets SET name = 'v'`,
		`DELETE FROM ` + name + `.widgets`,
		`SELECT * FROM ` + name + `.record_shares`,
	} {
		if err := asTenant(stmt); err != nil {
			t.Errorf("tenant role %q: %v", stmt, err)
		}
	}
	for _, table := range []string{"roles", "user_roles", "audit_log", "module_config", "sequences", "files", "record_activity", "saved_filters", "tenant_invitations", "event_log"} {
		requirePermissionDenied(t, asTenant(`SELECT 1 FROM `+name+`.`+table), "tenant role SELECT "+table)
	}
	requirePermissionDenied(t, asTenant(`INSERT INTO `+name+`.record_shares (model, record_id, shared_with_user_id, permission, shared_by) VALUES ('m', uuidv7(), uuidv7(), 'read', uuidv7())`), "tenant role INSERT record_shares")
}

func TestProvisioning_RecordSharesVisibleToTenantRoleOnlyForActingUser(t *testing.T) {
	schemaSync, engine := openRolePools(t)
	slug := provisionAsSchemaSync(t, schemaSync)
	table := tenantschema.Name(slug) + ".record_shares"
	alice, bob := uuid.New().String(), uuid.New().String()

	for _, user := range []string{alice, alice, bob} {
		if _, err := engine.Exec(`INSERT INTO `+table+` (model, record_id, shared_with_user_id, permission, shared_by) VALUES ('sales.widget', $1, $2, 'read', $3)`,
			uuid.New().String(), user, bob); err != nil {
			t.Fatalf("engine_user insert share: %v", err)
		}
	}

	count := func(role, user string) int {
		t.Helper()
		tx, err := engine.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`SELECT set_config('app.current_user_id', $1, true)`, user); err != nil {
			t.Fatalf("set acting user: %v", err)
		}
		if role != "" {
			if _, err := tx.Exec(`SELECT set_config('role', $1, true)`, role); err != nil {
				t.Fatalf("set role: %v", err)
			}
		}
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count shares: %v", err)
		}
		return n
	}

	if got := count("tenant_"+slug, alice); got != 2 {
		t.Errorf("tenant role acting as alice sees %d shares, want 2", got)
	}
	if got := count("tenant_"+slug, ""); got != 0 {
		t.Errorf("tenant role with no acting user sees %d shares, want 0", got)
	}
	if got := count("", alice); got != 3 {
		t.Errorf("engine_user sees %d shares, want 3", got)
	}
}

func TestOffboarding_DropsTenantRole(t *testing.T) {
	schemaSync, _ := openRolePools(t)
	slug := provisionAsSchemaSync(t, schemaSync)

	for range 2 {
		if err := tenantschema.Drop(t.Context(), schemaSync, slug); err != nil {
			t.Fatalf("Drop: %v", err)
		}
	}
	var roleExists, configExists bool
	if err := schemaSync.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1), EXISTS (SELECT 1 FROM partman.part_config WHERE parent_table LIKE $2)`,
		"tenant_"+slug, "tenant\\_"+slug+".%").Scan(&roleExists, &configExists); err != nil {
		t.Fatalf("check leftovers: %v", err)
	}
	if roleExists || configExists {
		t.Errorf("after Drop: role exists = %v, partman config exists = %v, want both false", roleExists, configExists)
	}
}

func TestCreateTenantSchema_SlugTooLongForARoleFailsBeforeCreatingTheSchema(t *testing.T) {
	schemaSync, _ := openRolePools(t)
	slug := "a" + strings.Repeat("b", 55) + "c" // 57 characters: valid for system.tenants, too long for tenant_{slug}
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), schemaSync, slug) })

	err := (&Activities{schemaSyncPool: schemaSync}).CreateTenantSchema(t.Context(), slug)
	if appErr, ok := errors.AsType[*temporal.ApplicationError](err); !ok || appErr.Type() != InvalidSlugErrorType || !appErr.NonRetryable() {
		t.Fatalf("CreateTenantSchema(57-char slug) = %v, want a non-retryable %s error", err, InvalidSlugErrorType)
	}
	var schemas int
	if err := schemaSync.QueryRow(`SELECT count(*) FROM pg_namespace WHERE nspname LIKE $1`, "tenant\\_abbbb%").Scan(&schemas); err != nil {
		t.Fatalf("count schemas: %v", err)
	}
	if schemas != 0 {
		t.Errorf("found %d schemas for the rejected slug, want 0", schemas)
	}
}

func TestDrop_RemovesPartmanTemplatesForAHyphenatedSlug(t *testing.T) {
	schemaSync, _ := openRolePools(t)
	slug := "role-drop-" + uniqueSlug(t)
	a := &Activities{schemaSyncPool: schemaSync}
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), schemaSync, slug) })
	if err := a.CreateTenantSchema(t.Context(), slug); err != nil {
		t.Fatalf("CreateTenantSchema: %v", err)
	}
	if err := a.CreateEngineTables(t.Context(), slug); err != nil {
		t.Fatalf("CreateEngineTables: %v", err)
	}

	var templates []string
	rows, err := schemaSync.Query(`SELECT template_table FROM partman.part_config WHERE parent_table LIKE $1`, "tenant\\_"+slug+".%")
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	for rows.Next() {
		var tmpl string
		if err := rows.Scan(&tmpl); err != nil {
			t.Fatalf("scan template: %v", err)
		}
		templates = append(templates, tmpl)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(templates) == 0 {
		t.Fatal("provisioning registered no pg_partman templates")
	}

	if err := tenantschema.Drop(t.Context(), schemaSync, slug); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	for _, tmpl := range templates {
		var exists bool
		if err := schemaSync.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname || '.' || c.relname = $1)`, tmpl).Scan(&exists); err != nil {
			t.Fatalf("check template %s: %v", tmpl, err)
		}
		if exists {
			t.Errorf("template %s still exists after Drop", tmpl)
		}
	}
}
