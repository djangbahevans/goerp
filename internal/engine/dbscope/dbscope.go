// Package dbscope implements multitenancy-internals.md §5's Layer 2 SQL
// validator: before executing any module-supplied SQL through host.db,
// reject any fully-qualified table reference outright — even one that
// happens to name the caller's own tenant schema — since modules must
// never hardcode a tenant schema name. Layer 1 (search_path, applied by
// the wasm package's applyTenantScope) is what actually resolves an
// unqualified "contacts" to the right tenant's table; this package is a
// defense-in-depth backstop against a module trying to bypass that
// resolution in the first place, not the primary isolation mechanism.
//
// Uses wasilibs/go-pgquery, a WASM (via wazero, already a goerp
// dependency for the module runtime) port of Postgres's own real parser —
// not pganalyze/pg_query_go directly, which wraps the same parser via
// CGO that this repo's CGO_ENABLED=0 build can't link.
package dbscope

import (
	"errors"
	"fmt"
	"reflect"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
)

// ErrQualifiedTableReference is wrapped by ValidateNoQualifiedTableRefs'
// returned error for a rejected fully-qualified table reference, so a
// caller (e.g. host.db.query/exec mapping this to the ABI's
// db.table_access_denied error code) can match on it with errors.Is
// rather than string-matching the message.
var ErrQualifiedTableReference = errors.New("fully-qualified table reference is not permitted")

// ValidateNoQualifiedTableRefs parses sql and rejects it if any table
// reference is schema-qualified: "SELECT * FROM contacts" is fine,
// "SELECT * FROM tenant_acmecorp.contacts" and "SELECT * FROM
// system.users" are both rejected — with no exception for a reference
// that happens to name the caller's own tenant schema
// (multitenancy-internals.md §5 Layer 2: "modules must never hardcode a
// tenant schema name").
//
// Never called against the schema-sync engine's own internal SQL, which
// reads information_schema under an elevated role and, unlike
// module-supplied SQL, legitimately needs schema-qualified references —
// this is the "elevated exception path" multitenancy-internals.md §5
// describes: the schema-sync engine simply never routes its own queries
// through this package, so no runtime bypass exists for it to opt into.
// Only host.db's own module-request-handler path is expected to call
// this function.
func ValidateNoQualifiedTableRefs(sql string) error {
	tree, err := pgquery.Parse(sql)
	if err != nil {
		return fmt.Errorf("parse SQL: %w", err)
	}

	if ref, ok := firstQualifiedTableRef(reflect.ValueOf(tree)); ok {
		return fmt.Errorf("%w: %q — tenant scoping is automatic, use unqualified table names only", ErrQualifiedTableReference, ref)
	}
	return nil
}

// firstQualifiedTableRef walks tree's protobuf AST for the first
// *pg_query.RangeVar with a non-empty Schemaname, returning it as
// "schema.table" and stopping there — ValidateNoQualifiedTableRefs only
// ever needs one match to reject the statement, so the walk
// short-circuits instead of collecting every match in a statement that
// might have many.
//
// A plain recursive reflect walk, not a hand-written visitor:
// pg_query_go's ParseResult is a deeply nested oneof tree — every
// statement type its own message, joins/CTEs/subqueries nested
// arbitrarily — and RangeVar is the one node type that ever carries a
// schema-qualified table name, so a generic walk that stops descending at
// *pg_query.RangeVar covers every statement shape Postgres's own grammar
// produces without hand-enumerating one case per statement/clause type
// the way a bespoke visitor would.
//
// Handles every reflect.Kind a protobuf-generated message tree actually
// produces: Pointer/Interface for optional/oneof fields, Struct for
// message fields, Slice/Array for repeated fields, and Map for the one
// shape protobuf ever generates for a map field (no map-typed field is
// currently reachable from ParseResult, but nothing here assumes that
// stays true — an unhandled Map would otherwise silently skip whatever it
// held, defeating a validator whose whole job is not missing a
// reference). Every other Kind — string, bool, the various int/float
// widths, and so on — is a scalar protobuf field, deliberately left as a
// no-op fallthrough rather than a panic on "unhandled Kind": those are
// the overwhelming majority of fields visited on any real input, and
// were never in-band candidates for a table reference either way.
func firstQualifiedTableRef(v reflect.Value) (string, bool) {
	if !v.IsValid() {
		return "", false
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return "", false
		}
		if rv, ok := reflect.TypeAssert[*pg_query.RangeVar](v); ok {
			if rv.Schemaname != "" {
				return rv.Schemaname + "." + rv.Relname, true
			}
			return "", false
		}
		return firstQualifiedTableRef(v.Elem())
	case reflect.Struct:
		for _, field := range v.Fields() {
			if !field.CanInterface() {
				continue
			}
			if ref, ok := firstQualifiedTableRef(field); ok {
				return ref, true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if ref, ok := firstQualifiedTableRef(v.Index(i)); ok {
				return ref, true
			}
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			if ref, ok := firstQualifiedTableRef(v.MapIndex(key)); ok {
				return ref, true
			}
		}
	}
	return "", false
}
