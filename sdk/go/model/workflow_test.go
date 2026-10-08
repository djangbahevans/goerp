package model

import (
	"reflect"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/events/def"
	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/vmihailenco/msgpack/v5"
)

func TestWorkflow_RoundTripsThroughMsgpack(t *testing.T) {
	f := Selection("draft", "confirmed", "cancelled", "done").
		Default("draft").
		Workflow(
			Transition("draft", "confirmed", "confirm").
				Requires(perm.Ref("sales:order:confirm")),
			Transition("confirmed", "cancelled", "cancel").
				Requires(perm.Ref("sales:order:cancel")),
			Transition("confirmed", "done", "complete").
				Requires(perm.Ref("sales:order:complete")).
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

	gated := Transition("draft", "confirmed", "confirm").Requires(perm.Ref("x:y:z"))
	if gated.Permission != "x:y:z" || gated.ConditionExpr != "" {
		t.Errorf("gated transition = %+v, want only Permission set", gated)
	}
}

func TestTransition_EmitsRoundTripsFieldSelection(t *testing.T) {
	event := def.Define[contactCreatedPayload]("sales.order.confirmed", def.Version(2), def.Description("Order confirmation"))
	transition := Transition("draft", "confirmed", "confirm").Emits(event)

	data, err := msgpack.Marshal(transition)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded WorkflowTransition
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Event == nil || !reflect.DeepEqual(decoded.Event, transition.Event) {
		t.Fatalf("event = %+v, want %+v", decoded.Event, transition.Event)
	}
	if decoded.Event.Name != event.Name() || decoded.Event.Version != 2 || decoded.Event.Description != "Order confirmation" || decoded.Event.ChangedFields {
		t.Fatalf("event = %+v", decoded.Event)
	}
	want := []LifecycleField{
		{Name: "Untagged", Record: "Untagged"},
		{Name: "contact_id", Record: "id"},
		{Name: "email", Record: "email"},
		{Name: "name", Record: "name"},
	}
	if !slices.Equal(decoded.Event.Fields, want) {
		t.Fatalf("fields = %+v, want %+v", decoded.Event.Fields, want)
	}
	if Transition("draft", "confirmed", "confirm").Event != nil {
		t.Fatal("bare transition declares an event")
	}
}

func TestTransition_EmitsRejectsNonStructPayload(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Emits accepted a non-struct payload")
		}
	}()
	Transition("draft", "confirmed", "confirm").Emits(def.Define[string]("sales.order.confirmed"))
}
