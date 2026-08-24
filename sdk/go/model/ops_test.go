package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestOp_ReservedNames(t *testing.T) {
	cases := []struct {
		op   Op
		want string
	}{
		{List, "list"},
		{Get, "get"},
		{Create, "create"},
		{Update, "update"},
		{Delete, "delete"},
		{Preview, "preview"},
	}
	for _, tc := range cases {
		if tc.op.Name != tc.want {
			t.Errorf("Name = %q, want %q", tc.op.Name, tc.want)
		}
		if tc.op.Condition != "" {
			t.Errorf("Condition = %q, want empty for a reserved constant", tc.op.Condition)
		}
	}
}

func TestOp_WithCondition_SetsConditionWithoutMutatingOriginal(t *testing.T) {
	withCond := List.WithCondition("record.owner_id = current_user.id")
	if withCond.Condition != "record.owner_id = current_user.id" {
		t.Errorf("Condition = %q, want the supplied expression", withCond.Condition)
	}
	if withCond.Name != "list" {
		t.Errorf("Name = %q, want %q", withCond.Name, "list")
	}
	if List.Condition != "" {
		t.Errorf("List.Condition = %q, want the package-level List to stay unmodified", List.Condition)
	}
}

func TestModelDeclaration_EnableOps_StoresDeclaredOps(t *testing.T) {
	d := Define("sales.order").EnableOps(List, Create)

	if len(d.EnabledOps) != 2 {
		t.Fatalf("got %d enabled ops, want 2", len(d.EnabledOps))
	}
	if d.EnabledOps[0].Name != "list" || d.EnabledOps[1].Name != "create" {
		t.Errorf("EnabledOps = %+v, want [list create]", d.EnabledOps)
	}
}

func TestModelDeclaration_NoEnableOpsCall_LeavesEmptyOpSet(t *testing.T) {
	d := Define("sales.order")
	if len(d.EnabledOps) != 0 {
		t.Errorf("got %d enabled ops with no EnableOps() call, want 0", len(d.EnabledOps))
	}
}

func TestModelDeclaration_EnableOps_ConditionRoundTripsThroughMsgpack(t *testing.T) {
	original := Define("legacy.inventory_item").
		EnableOps(Get, List.WithCondition("record.warehouse == current_user.warehouse"))

	data, err := msgpack.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded ModelDeclaration
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(decoded.EnabledOps) != 2 {
		t.Fatalf("got %d enabled ops, want 2", len(decoded.EnabledOps))
	}
	get, list := decoded.EnabledOps[0], decoded.EnabledOps[1]
	if get.Name != "get" || get.Condition != "" {
		t.Errorf("get op = %+v, want {get, no condition}", get)
	}
	if list.Name != "list" || list.Condition != "record.warehouse == current_user.warehouse" {
		t.Errorf("list op = %+v, want condition preserved", list)
	}
}
