package dbscope

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	pg_query "github.com/pganalyze/pg_query_go/v6"
)

func TestValidateTableRefs_AllowsUnqualifiedReferences(t *testing.T) {
	cases := []string{
		"SELECT id, name FROM contacts WHERE active = $1",
		"SELECT id FROM contacts WHERE active = $1",
		"INSERT INTO contacts (name) VALUES ($1) RETURNING id",
		"INSERT INTO contacts (name) VALUES ($1) ON CONFLICT (id) DO UPDATE SET name = excluded.name",
		"UPDATE contacts SET name = $1 WHERE id = $2 AND etag = $3",
		"DELETE FROM contacts WHERE id = $1",
		"WITH recent AS (SELECT id FROM orders WHERE created_at > $1) SELECT * FROM recent JOIN contacts ON contacts.id = recent.id",
		"SELECT a.id FROM contacts a JOIN users b ON a.id = b.contact_id",
		"SELECT 1",
	}
	for _, sql := range cases {
		if err := ValidateTableRefs(sql); err != nil {
			t.Errorf("ValidateTableRefs(%q) = %v, want nil", sql, err)
		}
	}
}

func TestValidateTableRefs_RejectsCrossSchemaReference(t *testing.T) {
	err := ValidateTableRefs("SELECT * FROM system.users")
	if err == nil {
		t.Fatal("expected an error for a cross-schema reference, got nil")
	}
	if !errors.Is(err, ErrQualifiedTableReference) {
		t.Errorf("error = %v, want it to wrap ErrQualifiedTableReference", err)
	}
	if !strings.Contains(err.Error(), "system.users") {
		t.Errorf("error = %q, want it to name the offending reference", err.Error())
	}
}

func TestValidateTableRefs_RejectsSameTenantQualifiedReference(t *testing.T) {
	// multitenancy-internals.md §5 Layer 2: even a reference that happens
	// to name the caller's own tenant schema is rejected outright, not
	// stripped and allowed to proceed — modules must never hardcode a
	// tenant schema name, so there is no legitimate reason for one to
	// appear in module-supplied SQL at all.
	err := ValidateTableRefs("SELECT * FROM tenant_acmecorp.contacts")
	if err == nil {
		t.Fatal("expected an error for a same-tenant-qualified reference, got nil")
	}
	if !errors.Is(err, ErrQualifiedTableReference) {
		t.Errorf("error = %v, want it to wrap ErrQualifiedTableReference", err)
	}
	if !strings.Contains(err.Error(), "tenant_acmecorp.contacts") {
		t.Errorf("error = %q, want it to name the offending reference", err.Error())
	}
}

func TestValidateTableRefs_RejectsQualifiedReferenceAmongUnqualifiedOnes(t *testing.T) {
	// A join where only one side is schema-qualified must still be
	// rejected — the validator has to inspect every table reference in
	// the statement, not just the first one.
	err := ValidateTableRefs("SELECT a.id FROM contacts a JOIN system.users b ON a.id = b.contact_id")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "system.users") {
		t.Errorf("error = %q, want it to name the offending reference", err.Error())
	}
}

// TestFirstDenial_WalksMapValues proves the Map case actually works: no
// field in pg_query_go's real ParseResult tree is map-typed today, so
// ValidateTableRefs' own tests never exercise it.
func TestFirstDenial_WalksMapValues(t *testing.T) {
	m := map[string]*pg_query.RangeVar{
		"x": {Schemaname: "system", Relname: "users"},
	}

	err := firstDenial(reflect.ValueOf(m))
	if !errors.Is(err, ErrQualifiedTableReference) || !strings.Contains(err.Error(), "system.users") {
		t.Errorf("firstDenial = %v, want a rejection of system.users", err)
	}
}

func TestValidateTableRefs_InvalidSQLReturnsParseError(t *testing.T) {
	err := ValidateTableRefs("SELECT FROM WHERE this is not valid SQL (((")
	if err == nil {
		t.Fatal("expected a parse error, got nil")
	}
	if errors.Is(err, ErrQualifiedTableReference) {
		t.Error("a syntax error must not be reported as a qualified-table-reference rejection")
	}
}

func TestValidateTableRefs_RejectsEngineOwnedTables(t *testing.T) {
	shapes := []string{
		"SELECT * FROM %s",
		"INSERT INTO %s DEFAULT VALUES",
		"UPDATE %s SET x = $1",
		"DELETE FROM %s",
		"SELECT c.id FROM contacts c JOIN %s e ON e.id = c.id",
		"SELECT id FROM contacts WHERE id IN (SELECT id FROM %s)",
		"WITH e AS (SELECT id FROM %s) SELECT * FROM e",
		"UPDATE contacts SET name = $1 FROM %s e WHERE e.id = contacts.id",
		"DELETE FROM contacts USING %s e WHERE e.id = contacts.id",
		"INSERT INTO contacts (id) SELECT id FROM %s",
		"SELECT * FROM contacts WHERE EXISTS (SELECT 1 FROM %s)",
		"SELECT (SELECT count(*) FROM %s) FROM contacts",
		"SELECT * FROM contacts, LATERAL (SELECT * FROM %s) e",
		"TABLE %s",
		"SELECT * FROM %s UNION SELECT * FROM contacts",
	}
	var names []string
	for _, g := range enginetables.Groups {
		for _, tbl := range g.Tables {
			names = append(names, tbl.Name)
			if tbl.Partitioned {
				names = append(names, tbl.Name+"_p20260901", tbl.Name+"_2026_09", tbl.Name+"_default")
			}
		}
	}
	for _, name := range names {
		for _, shape := range shapes {
			sql := fmt.Sprintf(shape, name)
			err := ValidateTableRefs(sql)
			if !errors.Is(err, ErrEngineOwnedTable) {
				t.Errorf("ValidateTableRefs(%q) = %v, want ErrEngineOwnedTable", sql, err)
				continue
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error = %q, want it to name %q", err.Error(), name)
			}
		}
	}
}

func TestValidateTableRefs_RejectsDeniedFunctions(t *testing.T) {
	cases := []string{
		"SELECT query_to_xml('select * from record_shares', true, false, '')",
		"SELECT pg_catalog.query_to_xml('select * from record_shares', true, false, '')",
		"SELECT query_to_xml_and_xmlschema('select * from user_roles', true, false, '')",
		"SELECT query_to_xmlschema('select * from user_roles', true, false, '')",
		"SELECT cursor_to_xml('c', 10, true, false, '')",
		"SELECT table_to_xml('record_shares', true, false, '')",
		"SELECT schema_to_xml(current_schema(), true, false, '')",
		"SELECT database_to_xml(true, false, '')",
		"SELECT ts_stat('select to_tsvector(body) from record_activity')",
		"SELECT ts_rewrite('a'::tsquery, 'select t, s from record_activity')",
		"SELECT set_config('search_path', 'system', true)",
		"SELECT id FROM contacts WHERE name = (SELECT query_to_xml('select 1', true, false, '')::text)",
		"UPDATE contacts SET name = query_to_xml('select * from roles', true, false, '')::text",
		"INSERT INTO contacts (name) VALUES (query_to_xml('select * from roles', true, false, '')::text)",
		"SELECT partman.run_maintenance()",
		"SELECT public.similarity(name, $1) FROM contacts",
	}
	for _, sql := range cases {
		if err := ValidateTableRefs(sql); !errors.Is(err, ErrDeniedFunction) {
			t.Errorf("ValidateTableRefs(%q) = %v, want ErrDeniedFunction", sql, err)
		}
	}
}

func TestValidateTableRefs_AllowsOrdinaryFunctions(t *testing.T) {
	cases := []string{
		"SELECT count(*), max(created_at) FROM contacts",
		"SELECT lower(name), coalesce(email, '') FROM contacts",
		"SELECT pg_catalog.lower(name) FROM contacts",
		"SELECT similarity(name, $1) FROM contacts WHERE name % $1",
		"SELECT current_setting('app.current_user_id', true)",
		"SELECT xmlelement(name contact, name) FROM contacts",
	}
	for _, sql := range cases {
		if err := ValidateTableRefs(sql); err != nil {
			t.Errorf("ValidateTableRefs(%q) = %v, want nil", sql, err)
		}
	}
}

func TestValidateTableRefs_RejectsPlannedEngineTables(t *testing.T) {
	for _, name := range []string{"notifications", "notification_preferences", "scheduled_activities", "view_overrides"} {
		if err := ValidateTableRefs("SELECT * FROM " + name); !errors.Is(err, ErrEngineOwnedTable) {
			t.Errorf("%s: err = %v, want ErrEngineOwnedTable", name, err)
		}
	}
}

func TestValidateTableRefs_RejectsSystemCatalogRelations(t *testing.T) {
	cases := []string{
		"UPDATE pg_settings SET setting = 'tenant_other, public' WHERE name = 'search_path'",
		"SELECT attname, most_common_vals FROM pg_stats WHERE tablename = 'user_roles'",
		"SELECT * FROM pg_class",
		"SELECT c.id FROM contacts c WHERE EXISTS (SELECT 1 FROM pg_roles)",
	}
	for _, sql := range cases {
		if err := ValidateTableRefs(sql); !errors.Is(err, ErrSystemCatalogReference) {
			t.Errorf("ValidateTableRefs(%q) = %v, want ErrSystemCatalogReference", sql, err)
		}
	}
}

func TestValidateTableRefs_RejectsJobQueueTables(t *testing.T) {
	cases := []string{
		"SELECT args FROM river_job",
		"DELETE FROM river_job WHERE kind = 'event_delivery'",
		"UPDATE river_queue SET paused_at = now()",
		"SELECT c.id FROM contacts c WHERE EXISTS (SELECT 1 FROM river_leader)",
		"WITH j AS (SELECT * FROM river_job) SELECT count(*) FROM j",
	}
	for _, sql := range cases {
		if err := ValidateTableRefs(sql); !errors.Is(err, ErrJobQueueTable) {
			t.Errorf("ValidateTableRefs(%q) = %v, want ErrJobQueueTable", sql, err)
		}
	}
}

func TestIsReservedTableName_JobQueueTables(t *testing.T) {
	for _, name := range []string{"river_job", "river_client_queue"} {
		if !IsReservedTableName(name) {
			t.Errorf("IsReservedTableName(%q) = false, want true", name)
		}
	}
	if IsReservedTableName("rivers") {
		t.Error(`IsReservedTableName("rivers") = true, want false`)
	}
}

func TestValidateTableRefs_AllowsModuleTablesPrefixedLikeEngineTables(t *testing.T) {
	cases := []string{
		"SELECT * FROM roles_catalog",
		"SELECT * FROM files_archive",
		"SELECT * FROM sequences_config",
		"SELECT * FROM audit_log_entries",
		"SELECT * FROM event_log_subscriptions",
		"SELECT * FROM audit_log_p",
	}
	for _, sql := range cases {
		if err := ValidateTableRefs(sql); err != nil {
			t.Errorf("ValidateTableRefs(%q) = %v, want nil", sql, err)
		}
	}
}

func TestValidateTableRefs_AllowsColumnsAndAliasesNamedLikeEngineOwnedTables(t *testing.T) {
	cases := []string{
		"SELECT record_activity FROM contacts",
		"SELECT c.id AS record_activity FROM contacts c",
		"SELECT record_activity.id FROM contacts AS record_activity",
	}
	for _, sql := range cases {
		if err := ValidateTableRefs(sql); err != nil {
			t.Errorf("ValidateTableRefs(%q) = %v, want nil", sql, err)
		}
	}
}
