// Package dbscope implements multitenancy-internals.md §5's Layer 2 SQL
// validator: before executing any module-supplied SQL through host.db,
// reject any fully-qualified table reference outright — even one that
// happens to name the caller's own tenant schema — since modules must
// never hardcode a tenant schema name — and any reference to an
// engine-owned per-tenant table or to a function that could reach one
// through a string argument. Layer 1 (search_path, applied by
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
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	pg_query "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
)

// ErrQualifiedTableReference is wrapped by ValidateTableRefs'
// returned error for a rejected fully-qualified table reference, so a
// caller (e.g. host.db.query/exec mapping this to the ABI's
// db.table_access_denied error code) can match on it with errors.Is
// rather than string-matching the message.
var ErrQualifiedTableReference = errors.New("fully-qualified table reference is not permitted")

// ErrEngineOwnedTable is wrapped by ValidateTableRefs' returned
// error for a reference to an engine-owned per-tenant table
// (enginetables.IsEngineOwned).
var ErrEngineOwnedTable = errors.New("engine-owned table is not accessible from module SQL")

// ErrSystemCatalogReference is wrapped by ValidateTableRefs' returned
// error for an unqualified reference to a pg_* relation.
var ErrSystemCatalogReference = errors.New("system catalog is not accessible from module SQL")

// ErrDeniedFunction is wrapped by ValidateTableRefs' returned error for a
// call to a function in deniedFunctions, or to a function qualified with
// a schema other than pg_catalog.
var ErrDeniedFunction = errors.New("function is not permitted in module SQL")

// deniedFunctions are built-in functions that execute SQL, or read a
// table, named by a string argument — which the parse-tree walk can't
// see into — plus set_config, which could repoint search_path away from
// the tenant schema for the rest of a transaction.
var deniedFunctions = map[string]bool{
	"query_to_xml":                  true,
	"query_to_xmlschema":            true,
	"query_to_xml_and_xmlschema":    true,
	"cursor_to_xml":                 true,
	"cursor_to_xmlschema":           true,
	"table_to_xml":                  true,
	"table_to_xmlschema":            true,
	"table_to_xml_and_xmlschema":    true,
	"schema_to_xml":                 true,
	"schema_to_xmlschema":           true,
	"schema_to_xml_and_xmlschema":   true,
	"database_to_xml":               true,
	"database_to_xmlschema":         true,
	"database_to_xml_and_xmlschema": true,
	"ts_stat":                       true,
	"ts_rewrite":                    true,
	"set_config":                    true,
}

// ValidateTableRefs parses sql and rejects it if any table
// reference is schema-qualified: "SELECT * FROM contacts" is fine,
// "SELECT * FROM tenant_acmecorp.contacts" and "SELECT * FROM
// system.users" are both rejected — with no exception for a reference
// that happens to name the caller's own tenant schema
// (multitenancy-internals.md §5 Layer 2: "modules must never hardcode a
// tenant schema name"). A reference to an engine-owned per-tenant table
// or one of its partitions, or to a pg_* system catalog relation, is
// rejected too, qualified or not, as is any
// call to a function in deniedFunctions or to a schema-qualified
// function outside pg_catalog (e.g. pg_partman's partman.* functions,
// which run DDL against a table named by string).
//
// Never called against the schema-sync engine's own internal SQL, which
// reads information_schema under an elevated role and, unlike
// module-supplied SQL, legitimately needs schema-qualified references —
// this is the "elevated exception path" multitenancy-internals.md §5
// describes: the schema-sync engine simply never routes its own queries
// through this package, so no runtime bypass exists for it to opt into.
// Only host.db's own module-request-handler path is expected to call
// this function.
func ValidateTableRefs(sql string) error {
	tree, err := pgquery.Parse(sql)
	if err != nil {
		return fmt.Errorf("parse SQL: %w", err)
	}
	return ValidateTreeTableRefs(tree)
}

// ValidateTreeTableRefs is ValidateTableRefs against
// an already-parsed tree, for a caller that also needs the same parse for
// something else (e.g. host.db.query's own DDL-keyword rejection) and
// would otherwise pay for parsing the same SQL text twice.
func ValidateTreeTableRefs(tree *pg_query.ParseResult) error {
	return firstDenial(reflect.ValueOf(tree))
}

func rangeVarDenial(rv *pg_query.RangeVar) error {
	if rv.Schemaname != "" {
		return fmt.Errorf("%w: %q — tenant scoping is automatic, use unqualified table names only", ErrQualifiedTableReference, rv.Schemaname+"."+rv.Relname)
	}
	if isSystemCatalogName(rv.Relname) {
		return fmt.Errorf("%w: %q", ErrSystemCatalogReference, rv.Relname)
	}
	if enginetables.IsEngineOwned(rv.Relname) {
		return fmt.Errorf("%w: %q", ErrEngineOwnedTable, rv.Relname)
	}
	return nil
}

// IsReservedTableName reports whether module SQL may never reference a
// table named name, so a module model must not take that name either.
func IsReservedTableName(name string) bool {
	return isSystemCatalogName(name) || enginetables.IsEngineOwned(name)
}

// isSystemCatalogName matches Postgres's pg_catalog relations, which
// resolve unqualified ahead of search_path. Some read or change state
// beyond the tenant schema: pg_stats samples column values of every
// table, and an UPDATE on pg_settings calls set_config.
func isSystemCatalogName(name string) bool {
	return strings.HasPrefix(name, "pg_")
}

func funcCallDenial(fc *pg_query.FuncCall) error {
	parts := make([]string, 0, len(fc.GetFuncname()))
	for _, n := range fc.GetFuncname() {
		parts = append(parts, n.GetString_().GetSval())
	}
	if len(parts) == 0 {
		return nil
	}
	name := parts[len(parts)-1]
	qualified := len(parts) > 1 && parts[len(parts)-2] != "pg_catalog"
	if qualified || deniedFunctions[name] {
		return fmt.Errorf("%w: %q", ErrDeniedFunction, strings.Join(parts, "."))
	}
	return nil
}

// firstDenial walks tree's protobuf AST for the first *pg_query.RangeVar
// rangeVarDenial rejects or *pg_query.FuncCall funcCallDenial rejects,
// returning that rejection and stopping there — one match is enough to
// reject the statement. A FuncCall that passes is still descended into,
// since its arguments can hold a subquery.
//
// A plain recursive reflect walk, not a hand-written visitor:
// pg_query_go's ParseResult is a deeply nested oneof tree — every
// statement type its own message, joins/CTEs/subqueries nested
// arbitrarily — so a generic walk covers every statement shape Postgres's
// own grammar produces without hand-enumerating one case per
// statement/clause type the way a bespoke visitor would.
//
// Handles every reflect.Kind a protobuf-generated message tree actually
// produces: Pointer/Interface for optional/oneof fields, Struct for
// message fields, Slice/Array for repeated fields, and Map for the one
// shape protobuf ever generates for a map field (no map-typed field is
// currently reachable from ParseResult, but an unhandled Map would
// silently skip whatever it held). Every other Kind is a scalar protobuf
// field and never a table reference.
func firstDenial(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		if rv, ok := reflect.TypeAssert[*pg_query.RangeVar](v); ok {
			return rangeVarDenial(rv)
		}
		if fc, ok := reflect.TypeAssert[*pg_query.FuncCall](v); ok {
			if err := funcCallDenial(fc); err != nil {
				return err
			}
		}
		return firstDenial(v.Elem())
	case reflect.Struct:
		for _, field := range v.Fields() {
			if !field.CanInterface() {
				continue
			}
			if err := firstDenial(field); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := firstDenial(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			if err := firstDenial(v.MapIndex(key)); err != nil {
				return err
			}
		}
	}
	return nil
}
