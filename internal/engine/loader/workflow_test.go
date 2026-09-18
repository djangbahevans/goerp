package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestValidateWorkflowTransitions_Valid(t *testing.T) {
	md := model.Define("order").Field("state", model.Selection("draft", "confirmed", "done").Workflow(
		model.Transition("draft", "confirmed", "confirm").Requires("sales:order:confirm"),
		model.Transition("confirmed", "done", "complete").
			Requires("sales:order:complete").
			Condition("record.amount_paid >= record.amount_total"),
	))

	if err := validateWorkflowTransitions([]model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("validateWorkflowTransitions: %v", err)
	}
}

func TestValidateWorkflowTransitions_RejectsNonSelectionField(t *testing.T) {
	md := model.Define("order").Field("state", model.Char())
	md.Fields[0].Def.WorkflowTransitions = []model.WorkflowTransition{
		model.Transition("draft", "confirmed", "confirm"),
	}

	err := validateWorkflowTransitions([]model.ModelDeclaration{*md})
	if err == nil || !strings.Contains(err.Error(), "only valid on a Selection field") {
		t.Fatalf("err = %v, want an error about Selection-only", err)
	}
}

func TestValidateWorkflowTransitions_RejectsFromNotInSelectionValues(t *testing.T) {
	md := model.Define("order").Field("state", model.Selection("draft", "done").Workflow(
		model.Transition("confirmed", "done", "complete"),
	))

	err := validateWorkflowTransitions([]model.ModelDeclaration{*md})
	if err == nil || !strings.Contains(err.Error(), "from state") {
		t.Fatalf("err = %v, want an error about the from state", err)
	}
}

func TestValidateWorkflowTransitions_RejectsToNotInSelectionValues(t *testing.T) {
	md := model.Define("order").Field("state", model.Selection("draft", "confirmed").Workflow(
		model.Transition("draft", "done", "confirm"),
	))

	err := validateWorkflowTransitions([]model.ModelDeclaration{*md})
	if err == nil || !strings.Contains(err.Error(), "to state") {
		t.Fatalf("err = %v, want an error about the to state", err)
	}
}

func TestValidateWorkflowTransitions_RejectsEmptyActionName(t *testing.T) {
	md := model.Define("order").Field("state", model.Selection("draft", "confirmed").Workflow(
		model.Transition("draft", "confirmed", ""),
	))

	err := validateWorkflowTransitions([]model.ModelDeclaration{*md})
	if err == nil || !strings.Contains(err.Error(), "non-empty action name") {
		t.Fatalf("err = %v, want an error about a non-empty action name", err)
	}
}

func TestValidateWorkflowTransitions_RejectsActionNameContainingSlash(t *testing.T) {
	md := model.Define("order").Field("state", model.Selection("draft", "confirmed").Workflow(
		model.Transition("draft", "confirmed", "confirm/force"),
	))

	err := validateWorkflowTransitions([]model.ModelDeclaration{*md})
	if err == nil || !strings.Contains(err.Error(), "must not contain") {
		t.Fatalf("err = %v, want an error about the action name containing a slash", err)
	}
}

func TestValidateWorkflowTransitions_RejectsDuplicateActionNameAcrossFields(t *testing.T) {
	md := model.Define("order").
		Field("state", model.Selection("draft", "confirmed").Workflow(
			model.Transition("draft", "confirmed", "confirm"),
		)).
		Field("approval_state", model.Selection("pending", "approved").Workflow(
			model.Transition("pending", "approved", "confirm"),
		))

	err := validateWorkflowTransitions([]model.ModelDeclaration{*md})
	if err == nil || !strings.Contains(err.Error(), "both declare a workflow transition named") {
		t.Fatalf("err = %v, want a duplicate-action-name error", err)
	}
}

func TestValidateWorkflowTransitions_RejectsUnparseableCondition(t *testing.T) {
	md := model.Define("order").Field("state", model.Selection("draft", "confirmed").Workflow(
		model.Transition("draft", "confirmed", "confirm").Condition("record.amount_paid >="),
	))

	err := validateWorkflowTransitions([]model.ModelDeclaration{*md})
	if err == nil || !strings.Contains(err.Error(), "condition failed to parse") {
		t.Fatalf("err = %v, want a condition-parse error", err)
	}
}

func TestValidateWorkflowTransitions_NoWorkflowIsANoop(t *testing.T) {
	md := model.Define("order").Field("state", model.Selection("draft", "confirmed"))

	if err := validateWorkflowTransitions([]model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("validateWorkflowTransitions: %v", err)
	}
}
