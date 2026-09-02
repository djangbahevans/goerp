package domain

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// CompileToRLS compiles a parsed domain expression AST into a boolean SQL
// expression suitable for a Postgres `CREATE POLICY ... USING (...)`/
// `WITH CHECK (...)` clause, per multitenancy-internals.md §5a. The
// expression is a static, schema-sync-time compilation of an ABAC policy's
// `condition` — there is no per-call user input to parameterize, so the
// output is a plain SQL string, not a parameterized fragment (contrast
// with the per-call `host.orm.search` domain-to-SQL compile target, which
// needs parameterization since it runs on caller-supplied values).
func CompileToRLS(expr Expr) (string, error) {
	return compileRLS(expr)
}

func compileRLS(expr Expr) (string, error) {
	switch e := expr.(type) {
	case RecordField:
		if e.Field == "" {
			return "", fmt.Errorf("domain: bare `record` has no SQL representation outside child_of/parent_of")
		}
		return quoteColumn(e.Field), nil

	case UserAttr:
		// NULLIF(...,'') guards a session where the GUC is set to an
		// empty string rather than left unset (e.g. a workflow activity
		// dispatched with no live user) — a bare ''::uuid cast errors
		// outright instead of evaluating the condition to false.
		switch e.Attr {
		case "id":
			return "NULLIF(current_setting('app.current_user_id', true), '')::uuid", nil
		case "contact_id":
			return "NULLIF(current_setting('app.current_user_contact_id', true), '')::uuid", nil
		default:
			return "", fmt.Errorf("domain: current_user.%s has no session variable to compile against in the RLS context yet", e.Attr)
		}

	case TenantAttr:
		return "", fmt.Errorf("domain: tenant.%s is only bound in tenant-only contexts (report_overrides[].condition), not in an ABAC policy condition", e.Field)

	case RoleCheck:
		return fmt.Sprintf("current_setting('app.current_user_roles', true) LIKE '%%%s%%'", EscapeSQLString(e.Role)), nil

	case PermCheck:
		return "", fmt.Errorf("domain: user_has_permission('%s') has no precomputed permission-set session variable to compile against yet", e.Perm)

	case Literal:
		return compileLiteral(e)

	case UnaryExpr:
		operand, err := compileRLS(e.Operand)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(NOT %s)", operand), nil

	case IsNullExpr:
		operand, err := compileRLS(e.Operand)
		if err != nil {
			return "", err
		}
		if e.Not {
			return fmt.Sprintf("(%s IS NOT NULL)", operand), nil
		}
		return fmt.Sprintf("(%s IS NULL)", operand), nil

	case InExpr:
		operand, err := compileRLS(e.Operand)
		if err != nil {
			return "", err
		}
		values := make([]string, len(e.Values))
		for i, v := range e.Values {
			compiled, err := compileRLS(v)
			if err != nil {
				return "", err
			}
			values[i] = compiled
		}
		return fmt.Sprintf("(%s IN (%s))", operand, strings.Join(values, ", ")), nil

	case BinaryExpr:
		return compileBinaryRLS(e)

	case TreeExpr:
		return "", fmt.Errorf("domain: %s is not yet supported — .Tree() field/ltree path column support doesn't exist yet", e.Op)

	default:
		return "", fmt.Errorf("domain: unsupported AST node %T", expr)
	}
}

func compileBinaryRLS(e BinaryExpr) (string, error) {
	switch e.Op {
	case "LIKE", "ILIKE":
		return "", fmt.Errorf("domain: %s is search-domain only and is rejected in ABAC policy conditions", e.Op)
	}

	left, err := compileRLS(e.Left)
	if err != nil {
		return "", err
	}
	right, err := compileRLS(e.Right)
	if err != nil {
		return "", err
	}

	switch e.Op {
	case "=", "!=", "<", ">", "<=", ">=", "AND", "OR":
		return fmt.Sprintf("(%s %s %s)", left, e.Op, right), nil
	default:
		return "", fmt.Errorf("domain: unsupported operator %q", e.Op)
	}
}

func compileLiteral(lit Literal) (string, error) {
	switch v := lit.Value.(type) {
	case nil:
		return "NULL", nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case Number:
		return string(v), nil
	case string:
		return "'" + EscapeSQLString(v) + "'", nil
	default:
		return "", fmt.Errorf("domain: unsupported literal type %T", v)
	}
}

// EscapeSQLString doubles embedded single quotes, per manifest-spec.md §8's
// "String literals in a domain follow the same escaping rule as SQL string
// literals" rule. The lexer has already stripped the source's own doubled
// quotes down to a single literal quote per manifest-spec.md §8, so this
// re-escapes before splicing into the compiled SQL string. Exported since
// internal/engine/schema's own RLS-widening policy compilation
// (goerp#471) needs the identical escaping for its own SQL literals.
func EscapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func quoteColumn(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
