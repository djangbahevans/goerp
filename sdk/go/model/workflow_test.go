package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestWorkflow_RoundTripsThroughMsgpack(t *testing.T) {
	f := Selection("draft", "confirmed", "cancelled", "done").
		Default("draft").
		Workflow(
			Transition("draft", "confirmed", "confirm").
				Requires("sales:order:confirm"),
			Transition("confirmed", "cancelled", "cancel").
				Requires("sales:order:cancel"),
			Transition("confirmed", "done", "complete").
				Requires("sales:order:complete").
				Condition("record.amount_paid >= record.amount_total"),
		)

	if len(f.WorkflowTransitions) != 3 {
		t.Fatalf("WorkflowTransitions = %d entries, want 3", len(f.WorkflowTransitions))
	}

	data, err := msgpack.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded FieldDef
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(decoded.WorkflowTransitions) != 3 {
		t.Fatalf("decoded WorkflowTransitions = %d entries, want 3", len(decoded.WorkflowTransitions))
	}

	complete := decoded.WorkflowTransitions[2]
	if complete.From != "confirmed" || complete.To != "done" || complete.ActionName != "complete" {
		t.Errorf("complete transition = %+v, want From=confirmed To=done ActionName=complete", complete)
	}
	if complete.Permission != "sales:order:complete" {
		t.Errorf("Permission = %q, want %q", complete.Permission, "sales:order:complete")
	}
	if complete.ConditionExpr != "record.amount_paid >= record.amount_total" {
		t.Errorf("ConditionExpr = %q, want the declared expression", complete.ConditionExpr)
	}
}

func TestWorkflow_NoCallHasNoTransitions(t *testing.T) {
	f := Selection("draft", "done")

	if f.WorkflowTransitions != nil {
		t.Errorf("WorkflowTransitions = %+v, want nil", f.WorkflowTransitions)
	}
}

func TestTransition_RequiresAndConditionAreIndependentlyOptional(t *testing.T) {
	bare := Transition("draft", "confirmed", "confirm")
	if bare.Permission != "" || bare.ConditionExpr != "" {
		t.Errorf("bare transition = %+v, want no permission or condition", bare)
	}

	gated := Transition("draft", "confirmed", "confirm").Requires("x:y:z")
	if gated.Permission != "x:y:z" || gated.ConditionExpr != "" {
		t.Errorf("gated transition = %+v, want only Permission set", gated)
	}
}
