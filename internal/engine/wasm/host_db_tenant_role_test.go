package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/dbscope"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5/pgconn"
	pgquery "github.com/wasilibs/go-pgquery"
)

// setupTenantRoleTest provisions two tenants the way provisioning does,
// each with a granted module table "widget", and returns a module context
// for the first.
func setupTenantRoleTest(t *testing.T) (primaryDB *sql.DB, slug, other string, mc *ModuleContext) {
	t.Helper()
	primaryDB = openTestPrimaryDB(t)
	slug = fmt.Sprintf("dbrole%d", time.Now().UnixNano())
	other = slug + "other"
	for _, s := range []string{slug, other} {
		createFixtureTenantSchema(t, primaryDB, s)
		if err := enginetables.CreateAll(t.Context(), primaryDB, s); err != nil {
			t.Fatalf("create engine tables: %v", err)
		}
		if _, err := primaryDB.Exec(`CREATE TABLE ` + tenantschema.Name(s) + `.widget (id UUID PRIMARY KEY, name TEXT)`); err != nil {
			t.Fatalf("create widget: %v", err)
		}
		grantFixtureTables(t, primaryDB, s, "widget")
	}
	return primaryDB, slug, other, newExecTestModuleContext(slug)
}

// runAsTenant runs stmt the way host.db runs module SQL — in a
// tenant-scoped transaction, as the tenant role — with no SQL validator in
// front of it.
func runAsTenant(t *testing.T, primaryDB *sql.DB, mc *ModuleContext, setup, stmt string) error {
	t.Helper()
	ctx := t.Context()
	tx, err := primaryDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := applyTenantScope(ctx, tx, mc); err != nil {
		t.Fatalf("applyTenantScope: %v", err)
	}
	if setup != "" {
		if _, err := tx.ExecContext(ctx, setup); err != nil {
			t.Fatalf("setup %q: %v", setup, err)
		}
	}
	var stmtErr error
	if hostErr := withTenantRole(ctx, tx, mc, func() *abiv1.HostError {
		rows, err := tx.QueryContext(ctx, stmt)
		if err != nil {
			stmtErr = err
			return &abiv1.HostError{Code: abiv1.ErrCodeQueryError, Message: err.Error()}
		}
		_, _, stmtErr = scanRowsToSlices(rows, maxQueryResultRows)
		return nil
	}); hostErr != nil && stmtErr == nil {
		t.Fatalf("withTenantRole: %+v", hostErr)
	}
	return stmtErr
}

func requirePermissionDenied(t *testing.T, err error, what string) {
	t.Helper()
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "42501" {
		t.Errorf("%s: got %v, want permission denied (42501)", what, err)
	}
}

func TestTenantRole_ModuleSQLCannotReachSystemEngineTablesOrOtherTenants(t *testing.T) {
	primaryDB, _, other, mc := setupTenantRoleTest(t)

	if err := runAsTenant(t, primaryDB, mc, "", `SELECT * FROM widget`); err != nil {
		t.Fatalf("own module table: %v", err)
	}
	for _, stmt := range []string{
		`SELECT * FROM system.users`,
		`SELECT * FROM system.tenants`,
		`SELECT * FROM system.river_job`,
		`SELECT * FROM audit_log`,
		`SELECT * FROM user_roles`,
		`SELECT * FROM ` + tenantschema.Name(other) + `.widget`,
		`SELECT * FROM ` + tenantschema.Name(other) + `.record_shares`,
	} {
		requirePermissionDenied(t, runAsTenant(t, primaryDB, mc, "", stmt), stmt)
	}

	// With search_path pointing at another tenant's schema, Postgres skips
	// the schema the role has no USAGE on, so the name doesn't resolve.
	misdirected := `SELECT set_config('search_path', '` + "tenant_" + other + `, public', true)`
	err := runAsTenant(t, primaryDB, mc, misdirected, `SELECT * FROM widget`)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || (pgErr.Code != "42P01" && pgErr.Code != "42501") {
		t.Errorf("misdirected search_path: got %v, want undefined_table or permission denied", err)
	}
}

func TestTenantRole_RoleChangesAreRejectedBeforePostgres(t *testing.T) {
	for _, stmt := range []string{`SET ROLE goerp`, `RESET ROLE`, `SET LOCAL ROLE NONE`} {
		tree, err := pgquery.Parse(stmt)
		if err != nil {
			t.Fatalf("parse %q: %v", stmt, err)
		}
		if requireSelectOnly(tree) == nil {
			t.Errorf("host.db.query accepted %q", stmt)
		}
		if _, err := parseExecStmt(tree); err == nil {
			t.Errorf("host.db.exec accepted %q", stmt)
		}
	}
	for _, stmt := range []string{
		`SELECT set_config('role', 'goerp', false)`,
		`UPDATE widget SET name = set_config('role', 'goerp', true)`,
	} {
		tree, err := pgquery.Parse(stmt)
		if err != nil {
			t.Fatalf("parse %q: %v", stmt, err)
		}
		if dbscope.ValidateTreeTableRefs(tree) == nil {
			t.Errorf("validator accepted %q", stmt)
		}
	}
}

func TestTenantRole_ShareableRowsVisibleOnlyToTheSharedWithUser(t *testing.T) {
	primaryDB, slug, _, mc := setupTenantRoleTest(t)
	name := tenantschema.Name(slug)
	shared, private := "40000000-0000-0000-0000-000000000001", "40000000-0000-0000-0000-000000000002"
	for _, stmt := range []string{
		`INSERT INTO ` + name + `.widget VALUES ('` + shared + `', 'shared'), ('` + private + `', 'private')`,
		`ALTER TABLE ` + name + `.widget ENABLE ROW LEVEL SECURITY`,
		`CREATE POLICY "testmodule:widget:none" ON ` + name + `.widget USING (false)`,
		`CREATE POLICY "testmodule:widget:__share_read" ON ` + name + `.widget FOR SELECT USING (EXISTS (
			SELECT 1 FROM ` + name + `.record_shares
			WHERE model = 'testmodule.widget' AND record_id = widget.id
			  AND shared_with_user_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid
			  AND permission = 'read'))`,
	} {
		if _, err := primaryDB.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := primaryDB.Exec(`INSERT INTO `+name+`.record_shares (model, record_id, shared_with_user_id, permission, shared_by) VALUES ('testmodule.widget', $1, $2, 'read', $2), ('testmodule.widget', $3, '00000000-0000-0000-0000-0000000000bb', 'read', $2)`,
		shared, mc.UserID, private); err != nil {
		t.Fatalf("insert shares: %v", err)
	}

	ctx := t.Context()
	tx, err := primaryDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := applyTenantScope(ctx, tx, mc); err != nil {
		t.Fatalf("applyTenantScope: %v", err)
	}
	var names []string
	if hostErr := withTenantRole(ctx, tx, mc, func() *abiv1.HostError {
		rows, err := tx.QueryContext(ctx, `SELECT name FROM widget ORDER BY name`)
		if err != nil {
			return &abiv1.HostError{Code: abiv1.ErrCodeQueryError, Message: err.Error()}
		}
		_, values, err := scanRowsToSlices(rows, maxQueryResultRows)
		if err != nil {
			return &abiv1.HostError{Code: abiv1.ErrCodeQueryError, Message: err.Error()}
		}
		for _, v := range values {
			names = append(names, v[0].(string))
		}
		return nil
	}); hostErr != nil {
		t.Fatalf("query as tenant role: %+v", hostErr)
	}
	if len(names) != 1 || names[0] != "shared" {
		t.Errorf("tenant role sees %v, want only the row shared with the acting user", names)
	}
}

// A statement that fails without aborting a borrowed transaction must
// leave it on the login role for whatever engine statement runs next.
func TestTenantRole_BorrowedTransactionReturnsToLoginRoleAfterAFailedStatement(t *testing.T) {
	primaryDB, _, _, mc := setupTenantRoleTest(t)
	ctx := t.Context()
	tx := registerTenantScopedTestTx(t, ctx, primaryDB, mc, "tx-role")
	defer func() { _ = tx.Rollback() }()

	var loginRole string
	if err := tx.QueryRowContext(ctx, `SELECT current_user`).Scan(&loginRole); err != nil {
		t.Fatalf("current_user: %v", err)
	}

	_, hostErr := DBExec(ctx, primaryDB, mc, abiv1.DBExecInput{
		SQL:  `INSERT INTO widget (id, name) VALUES (gen_random_uuid(), 'x')`,
		Opts: abiv1.DBExecOpts{Returning: "no_such_column"},
		TxID: "tx-role",
	})
	if hostErr == nil {
		t.Fatal("expected an error for a nonexistent returning column")
	}

	var current string
	if err := tx.QueryRowContext(ctx, `SELECT current_user`).Scan(&current); err != nil {
		t.Fatalf("current_user after failed exec: %v", err)
	}
	if current != loginRole {
		t.Errorf("current_user after failed exec = %q, want the login role %q", current, loginRole)
	}
}

// host.db.exec on an audited table writes audit_log as the login role in
// the same transaction, though the tenant role can't touch audit_log.
func TestTenantRole_AuditedExecStillWritesAuditLog(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	id := "50000000-0000-0000-0000-000000000001"
	if _, hostErr := DBExec(context.Background(), primaryDB, mc, abiv1.DBExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), 'audited')",
		Params: []any{id},
	}); hostErr != nil {
		t.Fatalf("DBExec: %+v", hostErr)
	}
	var n int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM `+tenantschema.Name(slug)+`.audit_log WHERE record_id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if n != 1 {
		t.Errorf("audit_log rows = %d, want 1", n)
	}
}
