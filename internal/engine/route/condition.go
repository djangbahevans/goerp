package route

import (
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/domain"
)

func (w *WorkflowManifest) compileCondition() error {
	if w.Condition == "" {
		return nil
	}
	expr, err := domain.Parse(w.Condition)
	if err != nil {
		return fmt.Errorf("parse condition: %w", err)
	}
	if err := domain.ValidateEval(expr); err != nil {
		return fmt.Errorf("validate condition: %w", err)
	}
	w.ConditionExpr = expr
	return nil
}
