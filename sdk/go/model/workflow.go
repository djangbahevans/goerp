package model

import "github.com/djangbahevans/goerp/sdk/go/perm"

// WorkflowTransition declares one state-machine transition a .Workflow()
// field allows — which state it moves from, which state it moves to, and
// the action name it registers under. Construct via
// Transition(from, to, actionName), then chain .Requires()/.Condition().
type WorkflowTransition struct {
	From       string `msgpack:"from"`
	To         string `msgpack:"to"`
	ActionName string `msgpack:"action_name"`

	// Permission gates who may invoke this transition — checked by the
	// auto-generated handler before anything else runs.
	Permission string `msgpack:"permission,omitempty"`

	// ConditionExpr is a domain expression gating the transition. The
	// generated transition handler does not evaluate it.
	ConditionExpr string `msgpack:"condition,omitempty"`
}

// Transition declares a workflow transition for use inside a Selection
// field's .Workflow(...) call.
func Transition(from, to, actionName string) WorkflowTransition {
	return WorkflowTransition{From: from, To: to, ActionName: actionName}
}

// Requires sets the permission a caller must hold to invoke this
// transition. It panics on the zero perm.Permission, which would leave the
// transition ungated.
func (t WorkflowTransition) Requires(permission perm.Permission) WorkflowTransition {
	if permission.Name() == "" {
		panic("model.WorkflowTransition.Requires: zero perm.Permission")
	}
	t.Permission = permission.Name()
	return t
}

// Condition attaches a domain-expression gate to this transition. The
// generated transition handler does not evaluate it.
func (t WorkflowTransition) Condition(expr string) WorkflowTransition {
	t.ConditionExpr = expr
	return t
}
