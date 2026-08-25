package engine

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// filterParamPattern matches erp-design.md §11.4's structured filter query
// param shape: filter[field] (implicit "eq") or filter[field][op].
var filterParamPattern = regexp.MustCompile(`^filter\[([^\[\]]+)\](?:\[([^\[\]]+)\])?$`)

// filterOperators maps a filter[field][op] query param's op suffix to the
// domain-expression-language comparison operator it compiles to.
// erp-design.md §11.4 documents only "gte" by example — the rest of this
// set is a deliberate, minimal choice matching the comparison operators
// internal/engine/domain's own grammar supports (ast.go's BinaryExpr Op
// set); "in" is handled separately below since it targets InExpr, not a
// BinaryExpr.
var filterOperators = map[string]string{
	"eq":   "=",
	"ne":   "!=",
	"gt":   ">",
	"gte":  ">=",
	"lt":   "<",
	"lte":  "<=",
	"like": "LIKE",
}

// resolveModelDecl resolves an ABI-level "{module}.{resource}" model name
// against modCtx's own declared models — the same "a module can only
// address its own models" resolution wasm.resolveModel performs
// internally (unexported there; mirrored here for dispatchORMList's own
// field-validation needs, which run before any host.orm call is made).
func resolveModelDecl(modCtx *wasm.ModuleContext, qualifiedName string) (model.ModelDeclaration, bool) {
	prefix := modCtx.ModuleName + "."
	if !strings.HasPrefix(qualifiedName, prefix) {
		return model.ModelDeclaration{}, false
	}
	bareName := strings.TrimPrefix(qualifiedName, prefix)
	for _, md := range modCtx.ModelDecls() {
		if md.Name == bareName {
			return md, true
		}
	}
	return model.ModelDeclaration{}, false
}

// escapeDomainLiteral doubles every single quote in value, the domain
// grammar's own SQL-style string-literal escaping (internal/engine/domain,
// lexer.go) — so a value containing a `'` can't break out of the
// generated expression.
func escapeDomainLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// compileListFilter parses ?filter[field]=value / ?filter[field][op]=value
// query parameters (erp-design.md §11.4) into a single domain-expression
// string, ANDed together — the form internal/engine/domain.Parse accepts,
// ready to pass as ORMSearchReadInput.Domain. Every filter value is
// compiled as a quoted string literal (matching both of erp-design.md
// §11.4's own examples, including a date value) — the domain-expression
// compiler's own SQL layer already parameterizes string literals and lets
// Postgres apply its normal implicit cast against a non-text column, so
// this compiler doesn't need to introspect each field's declared type
// just to decide how to quote it.
//
// An unknown field or operator is a *abi.HostError (orm.field_unknown /
// orm.domain_invalid) — never a silently-dropped filter. A query with no
// filter[...] params compiles to the unfiltered domain "true".
func compileListFilter(q url.Values, qualifiedModel string, md model.ModelDeclaration) (string, *abi.HostError) {
	declared := make(map[string]bool, len(md.Fields))
	for _, f := range md.Fields {
		declared[f.Name] = true
	}

	type clause struct {
		key  string
		expr string
	}
	var clauses []clause

	for key, values := range q {
		m := filterParamPattern.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		field, op := m[1], m[2]
		if op == "" {
			op = "eq"
		}
		if !declared[field] {
			return "", &abi.HostError{Code: abi.ErrCodeFieldUnknown, Message: "field " + field + " is not declared on " + qualifiedModel}
		}
		if len(values) == 0 || values[0] == "" {
			continue
		}
		value := values[0]

		if op == "in" {
			parts := strings.Split(value, ",")
			quoted := make([]string, len(parts))
			for i, p := range parts {
				quoted[i] = escapeDomainLiteral(p)
			}
			clauses = append(clauses, clause{key: key, expr: fmt.Sprintf("record.%s IN (%s)", field, strings.Join(quoted, ", "))})
			continue
		}

		sqlOp, ok := filterOperators[op]
		if !ok {
			return "", &abi.HostError{Code: abi.ErrCodeDomainInvalid, Message: "unknown filter operator " + op}
		}
		clauses = append(clauses, clause{key: key, expr: fmt.Sprintf("record.%s %s %s", field, sqlOp, escapeDomainLiteral(value))})
	}

	if len(clauses) == 0 {
		return "true", nil
	}

	// Sorted by query-param key for deterministic output — AND is
	// commutative, so this only affects the generated string's byte
	// content (test reproducibility), never the filtered result set.
	sort.Slice(clauses, func(i, j int) bool { return clauses[i].key < clauses[j].key })
	exprs := make([]string, len(clauses))
	for i, c := range clauses {
		exprs[i] = c.expr
	}
	return strings.Join(exprs, " AND "), nil
}
