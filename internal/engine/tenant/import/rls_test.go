package tenantimport

import (
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5/pgconn"
)

func openImportRoleDB(t *testing.T, role string, bypassRLS bool) *sql.DB {
	t.Helper()

	conn, err := db.New(fmt.Sprintf("postgres://%s:dev@localhost:15432/goerp", role))
	if err != nil {
		t.Fatalf("connect as %s: %v", role, err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	var currentRole string
	var superuser, bypass bool
	err = conn.QueryRowContext(t.Context(), `
		SELECT current_user, rolsuper, rolbypassrls
		FROM pg_roles WHERE rolname = current_user
	`).Scan(&currentRole, &superuser, &bypass)
	if err != nil {
		t.Fatalf("inspect database identity: %v", err)
	}

	if currentRole != role || superuser || bypass != bypassRLS {
		t.Fatalf("database identity = %s, superuser=%t, bypassrls=%t; want %s, false, %t",
			currentRole, superuser, bypass, role, bypassRLS)
	}

	return conn
}

func TestLoadModule_ImportsRLSProtectedRowsAsSchemaSyncUser(t *testing.T) {
	ctx := t.Context()
	adminDB := openTestPrimaryDB(t)
	slug := fmt.Sprintf("importrlstest%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)

	if _, err := adminDB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create import fixture schema: %v", err)
	}

	t.Cleanup(func() { _, _ = adminDB.Exec("DROP SCHEMA " + schema + " CASCADE") })

	_, err := adminDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE %s.widgets (id UUID PRIMARY KEY, name TEXT NOT NULL);
		ALTER TABLE %s.widgets ENABLE ROW LEVEL SECURITY;
		ALTER TABLE %s.widgets FORCE ROW LEVEL SECURITY;
		CREATE POLICY own_widget ON %s.widgets
			USING (id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
		GRANT USAGE ON SCHEMA %s TO engine_user, schema_sync_user;
		GRANT SELECT, INSERT ON %s.widgets TO engine_user, schema_sync_user;
	`, schema, schema, schema, schema, schema, schema))
	if err != nil {
		t.Fatalf("protect import fixture with RLS: %v", err)
	}

	mod := &module.LoadedModule{ModelDecls: []model.ModelDeclaration{widgetModelDecl()}}
	data := []byte(`{"model":"widget","record":{"id":"11111111-1111-1111-1111-111111111111","name":"Widget A"}}
{"model":"widget","record":{"id":"22222222-2222-2222-2222-222222222222","name":"Widget B"}}
`)

	ordinaryWorker := &Worker{RawDB: openImportRoleDB(t, "engine_user", false)}
	err = ordinaryWorker.loadModule(ctx, slug, mod, data)
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "42501" {
		t.Fatalf("ordinary engine import error = %v, want RLS denial (42501)", err)
	}

	var inserted int
	if err := adminDB.QueryRowContext(ctx, "SELECT count(*) FROM "+schema+".widgets").Scan(&inserted); err != nil {
		t.Fatalf("count rows after denied import: %v", err)
	}

	if inserted != 0 {
		t.Fatalf("rows inserted by denied import = %d, want 0", inserted)
	}

	worker := &Worker{RawDB: openImportRoleDB(t, "schema_sync_user", true)}
	for attempt := range 2 {
		if err := worker.loadModule(ctx, slug, mod, data); err != nil {
			t.Fatalf("import attempt %d as schema_sync_user: %v", attempt+1, err)
		}
	}

	rows, err := adminDB.QueryContext(ctx, "SELECT id::text, name FROM "+schema+".widgets")
	if err != nil {
		t.Fatalf("read imported rows: %v", err)
	}
	defer func() { _ = rows.Close() }()

	got := make(map[string]string)
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("scan imported row: %v", err)
		}

		got[id] = name
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read imported rows: %v", err)
	}

	expected := map[string]string{
		"11111111-1111-1111-1111-111111111111": "Widget A",
		"22222222-2222-2222-2222-222222222222": "Widget B",
	}
	if !maps.Equal(got, expected) {
		t.Fatalf("imported rows = %v, want %v", got, expected)
	}
}
