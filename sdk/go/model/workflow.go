package model

import (
	"github.com/djangbahevans/goerp/sdk/go/events/def"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

// WorkflowTransition declares an allowed state change and its action name.
type WorkflowTransition struct {
	From       string `msgpack:"from"`
	To         string `msgpack:"to"`
	ActionName string `msgpack:"action_name"`

	Permission string `msgpack:"permission,omitempty"`

	// ConditionExpr is a domain expression gating the transition. The
	// generated transition handler does not evaluate it.
	ConditionExpr string          `msgpack:"condition,omitempty"`
	Event         *LifecycleEvent `msgpack:"event,omitempty"`
}

// Emits declares one event emitted in the state write's transaction. Its
// payload must be a struct whose msgpack keys name model fields; a record
// tag selects a different source field. Payload values follow the caller's
// read access rules. Unknown fields fail module load; emission requires
// a model backed by a Postgres table.
func (t WorkflowTransition) Emits(event def.Definition) WorkflowTransition {
	t.Event = lifecycleEvent("WorkflowTransition.Emits", event, false)
	return t
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
