package domain

import "fmt"

// ValidateEval rejects constructs that cannot be evaluated against an in-memory record.
// It checks every branch, including operands that could be hidden by a boolean result.
func ValidateEval(expr Expr) error {
	switch e := expr.(type) {
	case RecordField:
		if e.Field == "" {
			return fmt.Errorf("domain: bare record has no value in memory")
		}
	case UserAttr, RoleCheck, PermCheck, Literal:
	case TenantAttr:
		return fmt.Errorf("domain: tenant.%s is not bound when evaluating a record", e.Field)
	case TreeExpr:
		return fmt.Errorf("domain: %s cannot be evaluated in memory", e.Op)
	case BinaryExpr:
		if e.Op == "LIKE" || e.Op == "ILIKE" {
			return fmt.Errorf("domain: %s cannot be evaluated in memory", e.Op)
		}
		if err := ValidateEval(e.Left); err != nil {
			return err
		}
		return ValidateEval(e.Right)
	case UnaryExpr:
		return ValidateEval(e.Operand)
	case IsNullExpr:
		return ValidateEval(e.Operand)
	case InExpr:
		if err := ValidateEval(e.Operand); err != nil {
			return err
		}
		for _, value := range e.Values {
			if err := ValidateEval(value); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("domain: unsupported AST node %T", expr)
	}
	return nil
}
