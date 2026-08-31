package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/vmihailenco/msgpack/v5"
)

var hostDBQueryCallerModule = buildHostCallerModule("host.db", []string{"begin", "commit", "rollback", "query", "query_replica"})

func newHostDBQueryCaller(t *testing.T, ctx context.Context, r *Runtime, mc *ModuleContext) *ModuleInstance {
	t.Helper()

	compiled, err := r.wazero.CompileModule(ctx, hostDBQueryCallerModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("query-caller-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	return inst
}

// newFixtureWidgetsTable creates a single-column table in the tenant
// schema slug already names, seeded with rows values — enough surface for
// host.db.query's own SELECT/column/row-shape behavior without pulling in
// host.orm or a compiled module fixture.
func newFixtureWidgetsTable(t *testing.T, conn *sql.DB, slug string, values ...string) {
	t.Helper()
	ctx := context.Background()
	schema := tenantschema.Name(slug)

	if _, err := conn.ExecContext(ctx, "CREATE TABLE "+schema+".widgets (name text)"); err != nil {
		t.Fatalf("create fixture table: %v", err)
	}
	for _, v := range values {
		if _, err := conn.ExecContext(ctx, "INSERT INTO "+schema+".widgets (name) VALUES ($1)", v); err != nil {
			t.Fatalf("insert fixture row: %v", err)
		}
	}
}

func TestHostDBQuery_UnqualifiedSelect_ReturnsRowsAndColumns(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbquerytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newFixtureWidgetsTable(t, primaryDB, slug, "gizmo", "gadget")

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_query", dbQueryInput{SQL: "SELECT name FROM widgets ORDER BY name"})
	if !env.OK {
		t.Fatalf("query failed: %+v", env.Error)
	}
	var out dbQueryOutput
	if err := msgpack.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if len(out.ColumnNames) != 1 || out.ColumnNames[0] != "name" {
		t.Errorf("ColumnNames = %v, want [name]", out.ColumnNames)
	}
	if len(out.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want 2", len(out.Rows))
	}
	if out.Rows[0][0] != "gadget" || out.Rows[1][0] != "gizmo" {
		t.Errorf("Rows = %v, want [[gadget] [gizmo]]", out.Rows)
	}
	if out.RowsAffected != 0 {
		t.Errorf("RowsAffected = %d, want 0 for a SELECT", out.RowsAffected)
	}
}

// TestHostDBQuery_UnqualifiedSelect_ResolvesAgainstCallersOwnTenant proves
// goerp#459's own integration acceptance criterion: an unqualified
// reference resolves through goerp#456's applyTenantScope search_path
// against the caller's own tenant schema, not a global or a different
// tenant's same-named table. Two tenants, each with their own "widgets"
// table holding different data, and a caller scoped to only one of them.
func TestHostDBQuery_UnqualifiedSelect_ResolvesAgainstCallersOwnTenant(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slugA := fmt.Sprintf("hostdbquerytesta%d", time.Now().UnixNano())
	slugB := fmt.Sprintf("hostdbquerytestb%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slugA)
	createFixtureTenantSchema(t, primaryDB, slugB)
	newFixtureWidgetsTable(t, primaryDB, slugA, "tenant-a-widget")
	newFixtureWidgetsTable(t, primaryDB, slugB, "tenant-b-widget")

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slugA, abi.CapDBRead, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_query", dbQueryInput{SQL: "SELECT name FROM widgets"})
	if !env.OK {
		t.Fatalf("query failed: %+v", env.Error)
	}
	var out dbQueryOutput
	if err := msgpack.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(out.Rows) != 1 || out.Rows[0][0] != "tenant-a-widget" {
		t.Errorf("Rows = %v, want [[tenant-a-widget]] — a caller scoped to tenant A must never see tenant B's row", out.Rows)
	}
}

func TestHostDBQuery_ParameterizedSelect(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbquerytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newFixtureWidgetsTable(t, primaryDB, slug, "gizmo", "gadget")

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_query", dbQueryInput{SQL: "SELECT name FROM widgets WHERE name = $1", Params: []any{"gizmo"}})
	if !env.OK {
		t.Fatalf("query failed: %+v", env.Error)
	}
	var out dbQueryOutput
	if err := msgpack.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(out.Rows) != 1 || out.Rows[0][0] != "gizmo" {
		t.Errorf("Rows = %v, want [[gizmo]]", out.Rows)
	}
}

func TestHostDBQuery_MissingCapabilityDenied(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbquerytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newFixtureWidgetsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBWrite, r.TxLimiter()) // no CapDBRead
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_query", dbQueryInput{SQL: "SELECT name FROM widgets"})
	if env.OK {
		t.Fatal("expected a capability-denied error, got success")
	}
	if env.Error.Code != abi.ErrCodeCapabilityDenied {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeCapabilityDenied)
	}
}

func TestHostDBQuery_RejectsSchemaQualifiedReference(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbquerytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newFixtureWidgetsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	cases := []string{
		"SELECT * FROM system.users",
		"SELECT * FROM " + tenantschema.Name(slug) + ".widgets",
	}
	for _, sql := range cases {
		env := callHost(t, ctx, inst, "call_query", dbQueryInput{SQL: sql})
		if env.OK {
			t.Errorf("query %q: expected an error, got success", sql)
			continue
		}
		if env.Error.Code != abi.ErrCodeTableAccessDenied {
			t.Errorf("query %q: Error.Code = %q, want %q", sql, env.Error.Code, abi.ErrCodeTableAccessDenied)
		}
	}
}

func TestHostDBQuery_RejectsDDL(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbquerytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	cases := []string{
		"CREATE TABLE evil (id int)",
		"DROP TABLE widgets",
		"ALTER TABLE widgets ADD COLUMN evil text",
		"TRUNCATE widgets",
	}
	for _, sql := range cases {
		env := callHost(t, ctx, inst, "call_query", dbQueryInput{SQL: sql})
		if env.OK {
			t.Errorf("query %q: expected an error, got success", sql)
			continue
		}
		if env.Error.Code != abi.ErrCodeQueryError {
			t.Errorf("query %q: Error.Code = %q, want %q", sql, env.Error.Code, abi.ErrCodeQueryError)
		}
	}
}

func TestHostDBQuery_TxID_RunsInsideExistingTransactionWithoutClosingIt(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbquerytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newFixtureWidgetsTable(t, primaryDB, slug, "gizmo")

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBRead|abi.CapDBWrite, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	beginEnv := callHost(t, ctx, inst, "call_begin", dbBeginInput{})
	if !beginEnv.OK {
		t.Fatalf("begin failed: %+v", beginEnv.Error)
	}
	var beginOut dbBeginOutput
	if err := msgpack.Unmarshal(beginEnv.Data, &beginOut); err != nil {
		t.Fatalf("unmarshal begin output: %v", err)
	}

	queryEnv := callHost(t, ctx, inst, "call_query", dbQueryInput{SQL: "SELECT name FROM widgets", TxID: beginOut.TxID})
	if !queryEnv.OK {
		t.Fatalf("query failed: %+v", queryEnv.Error)
	}
	var out dbQueryOutput
	if err := msgpack.Unmarshal(queryEnv.Data, &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(out.Rows) != 1 || out.Rows[0][0] != "gizmo" {
		t.Errorf("Rows = %v, want [[gizmo]]", out.Rows)
	}

	// The transaction must still be open and independently
	// committable — a single query run inside a borrowed transaction
	// must never commit or roll it back itself.
	if _, ok := mc.Transaction(beginOut.TxID); !ok {
		t.Fatal("expected the transaction to still be registered after call_query")
	}
	commitEnv := callHost(t, ctx, inst, "call_commit", dbTxIDInput{TxID: beginOut.TxID})
	if !commitEnv.OK {
		t.Fatalf("commit failed: %+v", commitEnv.Error)
	}
}

func TestHostDBQuery_TxIDNotFound(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbquerytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newFixtureWidgetsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_query", dbQueryInput{SQL: "SELECT name FROM widgets", TxID: "does-not-exist"})
	if env.OK {
		t.Fatal("expected an error, got success")
	}
	if env.Error.Code != abi.ErrCodeTransactionNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeTransactionNotFound)
	}
}

func TestHostDBQuery_ReadOnlyWithNoReplicaConfiguredIsRejected(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbquerytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newFixtureWidgetsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10) // never calls r.SetReplicaDB
	mc := newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_query", dbQueryInput{SQL: "SELECT name FROM widgets", Opts: dbQueryOpts{ReadOnly: true}})
	if env.OK {
		t.Fatal("expected an error, got success")
	}
	if env.Error.Code != abi.ErrCodeReplicaUnavailable {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeReplicaUnavailable)
	}
}

func TestHostDBQueryReplica_AlwaysRejectedWithNoReplicaConfiguredEvenWithoutOptsReadOnly(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbquerytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newFixtureWidgetsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_query_replica", dbQueryInput{SQL: "SELECT name FROM widgets"})
	if env.OK {
		t.Fatal("expected an error, got success")
	}
	if env.Error.Code != abi.ErrCodeReplicaUnavailable {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeReplicaUnavailable)
	}
}

func TestHostDBQuery_ReadOnlyRoutesToConfiguredReplica(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	replicaDB := openTestPrimaryDB(t) // no real replica in the dev stack; a second pool against the same Postgres proves the routing path, not replication lag
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbquerytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newFixtureWidgetsTable(t, primaryDB, slug, "gizmo")

	r := newHostDBTestRuntime(t, primaryDB, 10)
	r.SetReplicaDB(replicaDB)
	mc := newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	for _, tc := range []struct {
		name   string
		export string
		input  dbQueryInput
	}{
		{"read_only", "call_query", dbQueryInput{SQL: "SELECT name FROM widgets", Opts: dbQueryOpts{ReadOnly: true}}},
		{"query_replica", "call_query_replica", dbQueryInput{SQL: "SELECT name FROM widgets"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := callHost(t, ctx, inst, tc.export, tc.input)
			if !env.OK {
				t.Fatalf("query failed: %+v", env.Error)
			}
			var out dbQueryOutput
			if err := msgpack.Unmarshal(env.Data, &out); err != nil {
				t.Fatalf("unmarshal output: %v", err)
			}
			if len(out.Rows) != 1 || out.Rows[0][0] != "gizmo" {
				t.Errorf("Rows = %v, want [[gizmo]]", out.Rows)
			}
		})
	}
}

func TestHostDBQuery_TimeoutReturnsDBTimeoutAndRetry(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("hostdbquerytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newFixtureWidgetsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter())
	inst := newHostDBQueryCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_query", dbQueryInput{SQL: "SELECT pg_sleep(1)", Opts: dbQueryOpts{TimeoutMs: 50}})
	if env.OK {
		t.Fatal("expected a timeout error, got success")
	}
	if env.Error.Code != abi.ErrCodeDBTimeout {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeDBTimeout)
	}
	if !env.Error.Retry {
		t.Error("expected Retry to be true for a timeout")
	}
}

func TestScanRowsToSlices_ResultTooLarge(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	rows, err := primaryDB.QueryContext(ctx, "SELECT generate_series(1, 5)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	_, _, err = scanRowsToSlices(rows, 3)
	if err == nil {
		t.Fatal("expected errResultTooLarge, got nil")
	}
	if err != errResultTooLarge {
		t.Errorf("err = %v, want errResultTooLarge", err)
	}
}
