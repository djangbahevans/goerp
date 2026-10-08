package domain

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// CompileToSQL parameterizes literals in a domain WHERE clause and returns their
// arguments. Only record fields are bound; user/tenant attributes and role/permission
// predicates are rejected.
func CompileToSQL(expr Expr) (fragment string, args []any, err error) {
	c := &sqlCompiler{}
	frag, err := c.compile(expr)
	if err != nil {
		return "", nil, err
	}
	return frag, c.args, nil
}

// CompileToFilter compiles a parsed ABAC policy condition into a boolean SQL
// expression over the table's columns for one user, with everything bound to
// the user substituted: current_user.* become parameters, and
// user_has_role/user_has_permission become TRUE or FALSE. It is the condition
// the RLS policy installs, evaluated for env's user, and accepts what
// CompileToRLS accepts. Placeholders are numbered from paramsBefore+1, so
// fragments for several policies can share one parameter list.
func CompileToFilter(expr Expr, env Env, paramsBefore int) (fragment string, args []any, err error) {
	c := &sqlCompiler{env: &env, paramsBefore: paramsBefore}
	frag, err := c.compile(expr)
	if err != nil {
		return "", nil, err
	}
	return frag, c.args, nil
}

type sqlCompiler struct {
	args []any

	// env, when set, binds the current user's attributes (CompileToFilter);
	// nil is the search-domain context, where only record.* is bound.
	env *Env

	// paramsBefore is how many placeholders precede this compilation's own.
	paramsBefore int
}

func (c *sqlCompiler) bindArg(v any) string {
	c.args = append(c.args, v)
	return fmt.Sprintf("$%d", c.paramsBefore+len(c.args))
}

func (c *sqlCompiler) compile(expr Expr) (string, error) {
	switch e := expr.(type) {
	case RecordField:
		if e.Field == "" {
			return "", fmt.Errorf("domain: bare `record` has no SQL representation outside child_of/parent_of")
		}
		return quoteColumn(e.Field), nil

	case UserAttr:
		if c.env == nil {
			return "", fmt.Errorf("domain: current_user.%s is not bound in a host.orm.search domain — only record.* is available here", e.Attr)
		}
		return c.userAttr(e)

	case TenantAttr:
		return "", fmt.Errorf("domain: tenant.%s is not bound in a host.orm.search domain — only record.* is available here", e.Field)

	case RoleCheck:
		if c.env == nil {
			return "", fmt.Errorf("domain: user_has_role() is not bound in a host.orm.search domain — only record.* is available here")
		}
		return sqlBool(slices.Contains(c.env.Roles, e.Role)), nil

	case PermCheck:
		if c.env == nil {
			return "", fmt.Errorf("domain: user_has_permission() is not bound in a host.orm.search domain — only record.* is available here")
		}
		if c.env.HasPermission == nil {
			return "", fmt.Errorf("domain: user_has_permission('%s') has no permission resolver bound", e.Perm)
		}
		return sqlBool(c.env.HasPermission(e.Perm)), nil

	case Literal:
		return c.compileLiteral(e)

	case UnaryExpr:
		operand, err := c.compile(e.Operand)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(NOT %s)", operand), nil

	case IsNullExpr:
		operand, err := c.compile(e.Operand)
		if err != nil {
			return "", err
		}
		if e.Not {
			return fmt.Sprintf("(%s IS NOT NULL)", operand), nil
		}
		return fmt.Sprintf("(%s IS NULL)", operand), nil

	case InExpr:
		operand, err := c.compile(e.Operand)
		if err != nil {
			return "", err
		}
		values := make([]string, len(e.Values))
		for i, v := range e.Values {
			compiled, err := c.compile(v)
			if err != nil {
				return "", err
			}
			values[i] = compiled
		}
		return fmt.Sprintf("(%s IN (%s))", operand, strings.Join(values, ", ")), nil

	case BinaryExpr:
		return c.compileBinary(e)

	case TreeExpr:
		return "", fmt.Errorf("domain: %s is not yet supported — .Tree() field/ltree path column support doesn't exist yet", e.Op)

	default:
		return "", fmt.Errorf("domain: unsupported AST node %T", expr)
	}
}

func (c *sqlCompiler) compileBinary(e BinaryExpr) (string, error) {
	if c.env != nil && (e.Op == "LIKE" || e.Op == "ILIKE") {
		return "", fmt.Errorf("domain: %s is search-domain only and is rejected in ABAC policy conditions", e.Op)
	}

	left, err := c.compile(e.Left)
	if err != nil {
		return "", err
	}
	right, err := c.compile(e.Right)
	if err != nil {
		return "", err
	}

	switch e.Op {
	case "=", "!=", "<", ">", "<=", ">=", "LIKE", "ILIKE", "AND", "OR":
		return fmt.Sprintf("(%s %s %s)", left, e.Op, right), nil
	default:
		return "", fmt.Errorf("domain: unsupported operator %q", e.Op)
	}
}

func (c *sqlCompiler) compileLiteral(lit Literal) (string, error) {
	switch v := lit.Value.(type) {
	case nil:
		// No injection surface and no type-binding concern for these three
		// fixed literals — inlining avoids forcing a numeric/text OID onto
		// the placeholder for what is really a boolean/null constant.
		return "NULL", nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case Number:
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			return "", fmt.Errorf("domain: invalid numeric literal %q: %w", v, err)
		}
		return c.bindArg(f), nil
	case string:
		return c.bindArg(v), nil
	default:
		return "", fmt.Errorf("domain: unsupported literal type %T", v)
	}
}

// userAttr binds current_user.{attr} as a parameter; an empty value is NULL,
// like an unset session variable in the RLS form.
func (c *sqlCompiler) userAttr(e UserAttr) (string, error) {
	var value string
	switch e.Attr {
	case "id":
		value = c.env.UserID
	case "contact_id":
		value = c.env.ContactID
	case "tenant_id":
		value = c.env.TenantID
	default:
		return "", fmt.Errorf("domain: current_user.%s is not bound", e.Attr)
	}
	if value == "" {
		return "NULL", nil
	}
	return c.bindArg(value), nil
}

func sqlBool(v bool) string {
	if v {
		return "TRUE"
	}
	return "FALSE"
}
