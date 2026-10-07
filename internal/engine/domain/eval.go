package domain

import (
	"cmp"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"time"
)

// Env binds the names a condition can read when it is evaluated in memory:
// the record under test and the current user.
type Env struct {
	// Record maps column names to values. A key mapped to nil is a SQL NULL;
	// an absent key is an evaluation error.
	Record map[string]any

	UserID    string
	ContactID string
	TenantID  string
	Roles     []string

	// HasPermission reports whether the current user holds a permission.
	// user_has_permission is an evaluation error when it is nil.
	HasPermission func(name string) bool
}

// Eval evaluates expr against env and reports whether it is true. It follows
// SQL's three-valued logic, so a comparison with NULL is unknown and an
// unknown result is not true: the verdict agrees with what the RLS
// compilation of the same expression lets through. An empty user ID or
// contact ID is NULL, like an unset session variable in the RLS form.
//
// A field the record lacks, a comparison between incompatible types, and a
// construct with no in-memory meaning (LIKE, ILIKE, child_of, parent_of,
// tenant.*) are errors, never an allow.
func Eval(expr Expr, env Env) (bool, error) {
	v, err := eval(expr, env)
	if err != nil {
		return false, err
	}
	switch v := v.(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	default:
		return false, fmt.Errorf("domain: expression evaluates to %T, not a boolean", v)
	}
}

// eval returns nil for NULL (and unknown), a bool, a string, an int64, a
// float64 or a time.Time.
func eval(expr Expr, env Env) (any, error) {
	switch e := expr.(type) {
	case RecordField:
		if e.Field == "" {
			return nil, fmt.Errorf("domain: bare `record` has no value outside child_of/parent_of")
		}
		raw, ok := env.Record[e.Field]
		if !ok {
			return nil, fmt.Errorf("domain: record has no field %q", e.Field)
		}
		return normalize(raw)

	case UserAttr:
		var s string
		switch e.Attr {
		case "id":
			s = env.UserID
		case "contact_id":
			s = env.ContactID
		case "tenant_id":
			s = env.TenantID
		default:
			return nil, fmt.Errorf("domain: current_user.%s is not bound", e.Attr)
		}
		if s == "" {
			return nil, nil
		}
		return s, nil

	case TenantAttr:
		return nil, fmt.Errorf("domain: tenant.%s is only bound in tenant-only contexts, not when evaluating a record", e.Field)

	case RoleCheck:
		return slices.Contains(env.Roles, e.Role), nil

	case PermCheck:
		if env.HasPermission == nil {
			return nil, fmt.Errorf("domain: user_has_permission('%s') has no permission resolver bound", e.Perm)
		}
		return env.HasPermission(e.Perm), nil

	case Literal:
		return literalValue(e)

	case UnaryExpr:
		operand, err := eval(e.Operand, env)
		if err != nil {
			return nil, err
		}
		b, err := asBool(operand)
		if err != nil || b == nil {
			return nil, err
		}
		return !*b, nil

	case IsNullExpr:
		operand, err := eval(e.Operand, env)
		if err != nil {
			return nil, err
		}
		return (operand == nil) != e.Not, nil

	case InExpr:
		return evalIn(e, env)

	case BinaryExpr:
		return evalBinary(e, env)

	case TreeExpr:
		return nil, fmt.Errorf("domain: %s is not supported when evaluating a record in memory", e.Op)

	default:
		return nil, fmt.Errorf("domain: unsupported AST node %T", expr)
	}
}

func evalBinary(e BinaryExpr, env Env) (any, error) {
	switch e.Op {
	case "LIKE", "ILIKE":
		return nil, fmt.Errorf("domain: %s is search-domain only and is rejected when evaluating a record in memory", e.Op)
	case "AND", "OR":
		return evalLogical(e, env)
	}

	left, err := eval(e.Left, env)
	if err != nil {
		return nil, err
	}
	right, err := eval(e.Right, env)
	if err != nil {
		return nil, err
	}
	if left == nil || right == nil {
		return nil, nil
	}

	order, err := compare(left, right)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case "=":
		return order == 0, nil
	case "!=":
		return order != 0, nil
	case "<":
		return order < 0, nil
	case ">":
		return order > 0, nil
	case "<=":
		return order <= 0, nil
	case ">=":
		return order >= 0, nil
	default:
		return nil, fmt.Errorf("domain: unsupported operator %q", e.Op)
	}
}

// evalLogical evaluates AND and OR without short-circuiting past an error:
// both sides are evaluated so a malformed condition fails whatever the
// other side holds, matching SQL, which type-checks both operands.
func evalLogical(e BinaryExpr, env Env) (any, error) {
	left, err := evalBool(e.Left, env)
	if err != nil {
		return nil, err
	}
	right, err := evalBool(e.Right, env)
	if err != nil {
		return nil, err
	}

	decisive := e.Op == "OR" // the operand value that settles the result
	switch {
	case left != nil && *left == decisive, right != nil && *right == decisive:
		return decisive, nil
	case left == nil || right == nil:
		return nil, nil
	default:
		return !decisive, nil
	}
}

func evalBool(expr Expr, env Env) (*bool, error) {
	v, err := eval(expr, env)
	if err != nil {
		return nil, err
	}
	return asBool(v)
}

func asBool(v any) (*bool, error) {
	switch v := v.(type) {
	case nil:
		return nil, nil
	case bool:
		return &v, nil
	default:
		return nil, fmt.Errorf("domain: %T is not a boolean operand", v)
	}
}

func evalIn(e InExpr, env Env) (any, error) {
	operand, err := eval(e.Operand, env)
	if err != nil {
		return nil, err
	}

	sawNull := operand == nil
	matched := false
	for _, ve := range e.Values {
		v, err := eval(ve, env)
		if err != nil {
			return nil, err
		}
		if operand == nil || v == nil {
			sawNull = true
			continue
		}
		order, err := compare(operand, v)
		if err != nil {
			return nil, err
		}
		matched = matched || order == 0
	}

	switch {
	case matched:
		return true, nil
	case sawNull:
		return nil, nil
	default:
		return false, nil
	}
}

func literalValue(lit Literal) (any, error) {
	switch v := lit.Value.(type) {
	case nil, bool, string:
		return v, nil
	case Number:
		if i, err := strconv.ParseInt(string(v), 10, 64); err == nil {
			return i, nil
		}
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			return nil, fmt.Errorf("domain: invalid numeric literal %q: %w", v, err)
		}
		return f, nil
	default:
		return nil, fmt.Errorf("domain: unsupported literal type %T", v)
	}
}

// normalize maps a record value to the types eval produces.
func normalize(raw any) (any, error) {
	switch v := raw.(type) {
	case nil, bool, string, int64, float64, time.Time:
		return v, nil
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint:
		return normalizeUnsigned(uint64(v))
	case uint64:
		return normalizeUnsigned(v)
	case float32:
		return float64(v), nil
	case fmt.Stringer:
		if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
			return nil, nil
		}
		return v.String(), nil
	default:
		return nil, fmt.Errorf("domain: record value of type %T has no comparable form", raw)
	}
}

func normalizeUnsigned(v uint64) (any, error) {
	if v > math.MaxInt64 {
		return float64(v), nil
	}
	return int64(v), nil
}

// compare orders two non-NULL values of compatible types, returning an error
// for types SQL would not compare either. A string compared with a time is
// read as an RFC 3339 timestamp; a bare date is rejected because its meaning
// depends on a time zone the record does not carry.
func compare(a, b any) (int, error) {
	switch x := a.(type) {
	case bool:
		if y, ok := b.(bool); ok {
			return compareBool(x, y), nil
		}
	case string:
		switch y := b.(type) {
		case string:
			return cmp.Compare(x, y), nil
		case time.Time:
			t, err := parseTime(x)
			if err != nil {
				return 0, err
			}
			return t.Compare(y), nil
		}
	case int64:
		switch y := b.(type) {
		case int64:
			return cmp.Compare(x, y), nil
		case float64:
			return cmp.Compare(float64(x), y), nil
		}
	case float64:
		switch y := b.(type) {
		case int64:
			return cmp.Compare(x, float64(y)), nil
		case float64:
			return cmp.Compare(x, y), nil
		}
	case time.Time:
		switch y := b.(type) {
		case time.Time:
			return x.Compare(y), nil
		case string:
			t, err := parseTime(y)
			if err != nil {
				return 0, err
			}
			return x.Compare(t), nil
		}
	}
	return 0, fmt.Errorf("domain: cannot compare %T with %T", a, b)
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("domain: %q is not an RFC 3339 timestamp", s)
	}
	return t, nil
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case b:
		return -1
	default:
		return 1
	}
}
