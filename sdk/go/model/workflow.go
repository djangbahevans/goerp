package model

// WorkflowTransition declares one state-machine transition a .Workflow()
// field allows — which state it moves from, which state it moves to, and
// the action name it registers under (go-sdk-reference.md "Declarative
// workflow transitions"). Construct via Transition(from, to, actionName),
// then chain .Requires()/.Condition().
type WorkflowTransition struct {
	From       string `msgpack:"from"`
	To         string `msgpack:"to"`
	ActionName string `msgpack:"action_name"`

	// Permission gates who may invoke this transition — checked by the
	// auto-generated handler before anything else runs.
	Permission string `msgpack:"permission,omitempty"`

	// ConditionExpr is the domain-expression grammar go-sdk-reference.md
	// §22 specifies, captured here as a string. Server-side evaluation of
	// it at transition-invocation time is a separate, not-yet-built
	// mechanism (go-sdk-reference.md) — the auto-generated handler accepts
	// a Condition-declared transition today without enforcing it.
	ConditionExpr string `msgpack:"condition,omitempty"`
}

// Transition declares a workflow transition for use inside a Selection
// field's .Workflow(...) call.
func Transition(from, to, actionName string) WorkflowTransition {
	return WorkflowTransition{From: from, To: to, ActionName: actionName}
}

// Requires sets the permission a caller must hold to invoke this
// transition.
func (t WorkflowTransition) Requires(permission string) WorkflowTransition {
	t.Permission = permission
	return t
}

// Condition attaches a domain-expression gate to this transition. Its
// server-side evaluation is out of scope for the auto-generated handler
// today — see ConditionExpr's doc comment.
func (t WorkflowTransition) Condition(expr string) WorkflowTransition {
	t.ConditionExpr = expr
	return t
}
