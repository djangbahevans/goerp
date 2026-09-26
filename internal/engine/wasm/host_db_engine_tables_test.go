package wasm

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// setupEngineTablesTest provisions a tenant schema with every real
// engine-owned table (enginetables.CreateAll) plus the widget/gadget
// module tables, so a statement the validator let through would actually
// run rather than fail on a missing relation. Returns the canonical table
// names plus the real pg_partman partitions of the partitioned ones.
func setupEngineTablesTest(t *testing.T) (*sql.DB, string, []string) {
	t.Helper()
	ctx := t.Context()
	primaryDB := openTestPrimaryDB(t)
	slug := fmt.Sprintf("dbengtables%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	if err := enginetables.CreateAll(ctx, primaryDB, slug); err != nil {
		t.Fatalf("enginetables.CreateAll: %v", err)
	}

	assertOnlyCanonicalEngineTables(t, primaryDB, slug)

	schema := tenantschema.Name(slug)
	for _, stmt := range []string{
		`CREATE TABLE ` + schema + `.gadget (id UUID PRIMARY KEY, name TEXT)`,
		`CREATE TABLE ` + schema + `.widget (
			id UUID PRIMARY KEY,
			tenant_id UUID NOT NULL,
			etag TEXT NOT NULL DEFAULT '',
			name TEXT UNIQUE,
			secret TEXT,
			parent_id UUID REFERENCES ` + schema + `.gadget(id)
		)`,
	} {
		if _, err := primaryDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("create module table: %v", err)
		}
	}
	grantFixtureTables(t, primaryDB, slug, "gadget", "widget")

	var names []string
	partitioned := 0
	for _, g := range enginetables.Groups {
		for _, tbl := range g.Tables {
			names = append(names, tbl.Name)
			if !tbl.Partitioned {
				continue
			}
			partitioned++
			var partition string
			err := primaryDB.QueryRowContext(ctx, `
				SELECT c.relname FROM pg_inherits i
				JOIN pg_class c ON c.oid = i.inhrelid
				JOIN pg_class p ON p.oid = i.inhparent
				JOIN pg_namespace n ON n.oid = p.relnamespace
				WHERE n.nspname = $1 AND p.relname = $2
				LIMIT 1`, "tenant_"+slug, tbl.Name).Scan(&partition)
			if err != nil {
				t.Fatalf("look up a partition of %s: %v", tbl.Name, err)
			}
			names = append(names, partition)
		}
	}
	if partitioned == 0 {
		t.Fatal("enginetables.Groups has no partitioned table to cover")
	}
	return primaryDB, slug, names
}

// assertOnlyCanonicalEngineTables fails if CreateAll left any table in the
// tenant schema, other than a partition, that enginetables.Groups doesn't
// list — one the validator and loader would therefore not block.
func assertOnlyCanonicalEngineTables(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	listed := map[string]bool{}
	for _, g := range enginetables.Groups {
		for _, tbl := range g.Tables {
			listed[tbl.Name] = true
		}
	}
	rows, err := conn.QueryContext(t.Context(), `
		SELECT c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition`, "tenant_"+slug)
	if err != nil {
		t.Fatalf("list tenant tables: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan tenant table: %v", err)
		}
		if !listed[name] {
			t.Errorf("CreateAll created %q, which enginetables.Groups doesn't list", name)
		}
		delete(listed, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list tenant tables: %v", err)
	}
	for name := range listed {
		t.Errorf("enginetables.Groups lists %q, which CreateAll didn't create", name)
	}
}

func TestHostDB_RejectsEngineOwnedTables(t *testing.T) {
	primaryDB, slug, names := setupEngineTablesTest(t)
	ctx := t.Context()

	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostDBQueryCaller(t, ctx, r, newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter()))
	execCtx := newExecTestModuleContext(slug)

	for _, name := range names {
		for _, sql := range []string{
			"SELECT * FROM " + name,
			"SELECT w.id FROM widget w JOIN " + name + " e ON true",
			"SELECT id FROM widget WHERE EXISTS (SELECT 1 FROM " + name + ")",
		} {
			env := callHost(t, ctx, inst, "call_query", abiv1.DBQueryInput{SQL: sql})
			if env.OK || env.Error.Code != abiv1.ErrCodeTableAccessDenied {
				t.Errorf("query %q: envelope = %+v, want %q", sql, env, abiv1.ErrCodeTableAccessDenied)
			}
		}

		for _, sql := range []string{
			"DELETE FROM " + name,
			"UPDATE widget SET name = 'x' FROM " + name + " e WHERE false",
			"DELETE FROM widget WHERE EXISTS (SELECT 1 FROM " + name + ")",
		} {
			_, hostErr := DBExec(ctx, primaryDB, execCtx, abiv1.DBExecInput{SQL: sql})
			if hostErr == nil || hostErr.Code != abiv1.ErrCodeTableAccessDenied {
				t.Errorf("exec %q: error = %v, want %q", sql, hostErr, abiv1.ErrCodeTableAccessDenied)
			}
		}

		sql := "DELETE FROM widget WHERE id = $1 AND EXISTS (SELECT 1 FROM " + name + ")"
		_, hostErr := DBExecBatch(ctx, primaryDB, execCtx, abiv1.DBExecBatchInput{
			SQL:       sql,
			ParamSets: [][]any{{"11111111-1111-1111-1111-111111111111"}},
		})
		if hostErr == nil || hostErr.Code != abiv1.ErrCodeTableAccessDenied {
			t.Errorf("exec_batch %q: error = %v, want %q", sql, hostErr, abiv1.ErrCodeTableAccessDenied)
		}
	}
}

func TestHostDB_RejectsSQLExecutingFunctionsAndCatalogs(t *testing.T) {
	primaryDB, slug, _ := setupEngineTablesTest(t)
	ctx := t.Context()

	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostDBQueryCaller(t, ctx, r, newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter()))

	for _, sql := range []string{
		"SELECT query_to_xml('select * from record_shares', true, false, '')",
		"SELECT query_to_xml_and_xmlschema('select * from user_roles', true, false, '')",
		"SELECT table_to_xml('roles', true, false, '')",
		"SELECT schema_to_xml(current_schema(), true, false, '')",
		"SELECT set_config('search_path', 'system', true)",
		"SELECT most_common_vals FROM pg_stats WHERE tablename = 'user_roles'",
		"SELECT partman.show_partitions('tenant_" + slug + ".audit_log')",
	} {
		env := callHost(t, ctx, inst, "call_query", abiv1.DBQueryInput{SQL: sql})
		if env.OK || env.Error.Code != abiv1.ErrCodeTableAccessDenied {
			t.Errorf("query %q: envelope = %+v, want %q", sql, env, abiv1.ErrCodeTableAccessDenied)
		}
	}

	for _, sql := range []string{
		"UPDATE widget SET secret = query_to_xml('select * from record_shares', true, false, '')::text",
		"UPDATE pg_settings SET setting = 'system, public' WHERE name = 'search_path'",
	} {
		_, hostErr := DBExec(ctx, primaryDB, newExecTestModuleContext(slug), abiv1.DBExecInput{SQL: sql})
		if hostErr == nil || hostErr.Code != abiv1.ErrCodeTableAccessDenied {
			t.Errorf("exec %q: error = %v, want %q", sql, hostErr, abiv1.ErrCodeTableAccessDenied)
		}
	}
}

func TestHostDB_AllowsModuleTablesAlongsideEngineTables(t *testing.T) {
	primaryDB, slug, _ := setupEngineTablesTest(t)
	ctx := t.Context()
	execCtx := newExecTestModuleContext(slug)

	gadgetID := "20000000-0000-0000-0000-000000000001"
	if _, hostErr := DBExec(ctx, primaryDB, execCtx, abiv1.DBExecInput{
		SQL:    "INSERT INTO gadget (id, name) VALUES ($1, 'g')",
		Params: []any{gadgetID},
	}); hostErr != nil {
		t.Fatalf("insert gadget: %+v", hostErr)
	}
	if _, hostErr := DBExec(ctx, primaryDB, execCtx, abiv1.DBExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name, parent_id) VALUES (gen_random_uuid(), gen_random_uuid(), 'w', $1)",
		Params: []any{gadgetID},
	}); hostErr != nil {
		t.Fatalf("insert widget: %+v", hostErr)
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	inst := newHostDBQueryCaller(t, ctx, r, newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter()))
	env := callHost(t, ctx, inst, "call_query", abiv1.DBQueryInput{
		SQL: "SELECT w.name, g.name FROM widget w JOIN gadget g ON g.id = w.parent_id",
	})
	if !env.OK {
		t.Fatalf("join query: %+v", env.Error)
	}
}
