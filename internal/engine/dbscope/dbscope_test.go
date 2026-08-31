package dbscope

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

func TestValidateNoQualifiedTableRefs_AllowsUnqualifiedReferences(t *testing.T) {
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
		if err := ValidateNoQualifiedTableRefs(sql); err != nil {
			t.Errorf("ValidateNoQualifiedTableRefs(%q) = %v, want nil", sql, err)
		}
	}
}

func TestValidateNoQualifiedTableRefs_RejectsCrossSchemaReference(t *testing.T) {
	err := ValidateNoQualifiedTableRefs("SELECT * FROM system.users")
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

func TestValidateNoQualifiedTableRefs_RejectsSameTenantQualifiedReference(t *testing.T) {
	// multitenancy-internals.md §5 Layer 2: even a reference that happens
	// to name the caller's own tenant schema is rejected outright, not
	// stripped and allowed to proceed — modules must never hardcode a
	// tenant schema name, so there is no legitimate reason for one to
	// appear in module-supplied SQL at all.
	err := ValidateNoQualifiedTableRefs("SELECT * FROM tenant_acmecorp.contacts")
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

func TestValidateNoQualifiedTableRefs_RejectsQualifiedReferenceAmongUnqualifiedOnes(t *testing.T) {
	// A join where only one side is schema-qualified must still be
	// rejected — the validator has to inspect every table reference in
	// the statement, not just the first one.
	err := ValidateNoQualifiedTableRefs("SELECT a.id FROM contacts a JOIN system.users b ON a.id = b.contact_id")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "system.users") {
		t.Errorf("error = %q, want it to name the offending reference", err.Error())
	}
}

// TestFirstQualifiedTableRef_WalksMapValues proves the Map case actually
// works: no field in pg_query_go's real ParseResult tree is map-typed
// today, so ValidateNoQualifiedTableRefs' own tests never exercise it —
// this synthesizes a map holding a *pg_query.RangeVar directly, the shape
// firstQualifiedTableRef's own doc comment says protobuf map fields take.
func TestFirstQualifiedTableRef_WalksMapValues(t *testing.T) {
	m := map[string]*pg_query.RangeVar{
		"x": {Schemaname: "system", Relname: "users"},
	}

	ref, ok := firstQualifiedTableRef(reflect.ValueOf(m))
	if !ok {
		t.Fatal("expected a qualified reference to be found inside the map, got none")
	}
	if ref != "system.users" {
		t.Errorf("ref = %q, want %q", ref, "system.users")
	}
}

func TestValidateNoQualifiedTableRefs_InvalidSQLReturnsParseError(t *testing.T) {
	err := ValidateNoQualifiedTableRefs("SELECT FROM WHERE this is not valid SQL (((")
	if err == nil {
		t.Fatal("expected a parse error, got nil")
	}
	if errors.Is(err, ErrQualifiedTableReference) {
		t.Error("a syntax error must not be reported as a qualified-table-reference rejection")
	}
}
