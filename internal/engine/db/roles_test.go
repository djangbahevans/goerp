package db

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Tables schema_sync_user creates in system are DML-only for engine_user
// (data-layer.md §2.2), through the default privileges
// docker/postgres-initdb sets.
func TestSystemSchema_EngineRoleHasDMLButNoDDL(t *testing.T) {
	openTestPool(t)
	schemaSync, err := New("postgres://schema_sync_user:dev@localhost:55432/goerp")
	if err != nil {
		t.Fatalf("connect as schema_sync_user (recreate the dev stack with docker compose down -v): %v", err)
	}
	t.Cleanup(func() { _ = schemaSync.Close() })
	engine, err := New("postgres://engine_user:dev@localhost:55432/goerp")
	if err != nil {
		t.Fatalf("connect as engine_user (recreate the dev stack with docker compose down -v): %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	table := fmt.Sprintf("system.roles_test_%d", time.Now().UnixNano())
	if _, err := schemaSync.Exec(`CREATE TABLE ` + table + ` (id bigserial PRIMARY KEY, v text)`); err != nil {
		t.Fatalf("schema_sync_user CREATE TABLE in system: %v", err)
	}
	t.Cleanup(func() { _, _ = schemaSync.Exec(`DROP TABLE IF EXISTS ` + table) })

	if _, err := engine.Exec(`INSERT INTO ` + table + ` (v) VALUES ('a')`); err != nil {
		t.Fatalf("engine_user INSERT (table and sequence): %v", err)
	}
	if _, err := engine.Exec(`UPDATE ` + table + ` SET v = 'b'`); err != nil {
		t.Fatalf("engine_user UPDATE: %v", err)
	}
	if _, err := engine.Exec(`DELETE FROM ` + table); err != nil {
		t.Fatalf("engine_user DELETE: %v", err)
	}

	for _, stmt := range []string{
		`CREATE TABLE system.engine_ddl (id int)`,
		`ALTER TABLE ` + table + ` ADD COLUMN extra int`,
		`DROP TABLE ` + table,
	} {
		_, err := engine.Exec(stmt)
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
			t.Errorf("engine_user %q: got %v, want permission denied (42501)", stmt, err)
		}
	}
}

func TestTenantRoleFunctions(t *testing.T) {
	openTestPool(t)
	schemaSync, err := New("postgres://schema_sync_user:dev@localhost:55432/goerp")
	if err != nil {
		t.Fatalf("connect as schema_sync_user (recreate the dev stack with docker compose down -v): %v", err)
	}
	t.Cleanup(func() { _ = schemaSync.Close() })
	engine, err := New("postgres://engine_user:dev@localhost:55432/goerp")
	if err != nil {
		t.Fatalf("connect as engine_user (recreate the dev stack with docker compose down -v): %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	slug := fmt.Sprintf("roles-%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = schemaSync.Exec(`SELECT system.drop_tenant_role($1)`, slug) })
	for range 2 {
		if _, err := schemaSync.Exec(`SELECT system.create_tenant_role($1)`, slug); err != nil {
			t.Fatalf("create_tenant_role: %v", err)
		}
	}

	var current string
	err = func() error {
		tx, err := engine.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(`SELECT set_config('role', $1, true)`, "tenant_"+slug); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT current_user`).Scan(&current)
	}()
	if err != nil || current != "tenant_"+slug {
		t.Fatalf("engine_user SET ROLE tenant_%s: current_user = %q, err = %v", slug, current, err)
	}

	for _, bad := range []string{"Bad_Slug", "x", strings.Repeat("a", 57)} {
		if _, err := schemaSync.Exec(`SELECT system.create_tenant_role($1)`, bad); err == nil {
			t.Errorf("create_tenant_role(%q) succeeded, want invalid tenant slug", bad)
		}
	}
	_, err = engine.Exec(`SELECT system.create_tenant_role($1)`, slug+"x")
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
		t.Errorf("engine_user create_tenant_role: got %v, want permission denied (42501)", err)
	}

	if _, err := schemaSync.Exec(`SELECT system.drop_tenant_role($1)`, slug); err != nil {
		t.Fatalf("drop_tenant_role: %v", err)
	}
	var exists bool
	if err := schemaSync.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, "tenant_"+slug).Scan(&exists); err != nil || exists {
		t.Errorf("tenant role still exists after drop_tenant_role (err %v)", err)
	}
}
