package orm

import (
	"reflect"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

type stockUnits int32

func TestMutateOptions_BuildOpsAndGuard(t *testing.T) {
	var spec mutateSpec
	for _, opt := range []MutateOption{
		Increment("reserved", int64(4)),
		Decrement("on_hand", 4),
		Decrement("weight", 0.5),
		Increment("units", stockUnits(2)),
		Where("record.on_hand >= 4"),
	} {
		opt(&spec)
	}

	wantOps := []abi.ORMMutateOp{
		{Field: "reserved", Delta: int64(4)},
		{Field: "on_hand", Delta: -4},
		{Field: "weight", Delta: -0.5},
		{Field: "units", Delta: stockUnits(2)},
	}
	if !reflect.DeepEqual(spec.ops, wantOps) {
		t.Errorf("ops = %#v, want %#v", spec.ops, wantOps)
	}
	if spec.guard != "record.on_hand >= 4" {
		t.Errorf("guard = %q, want the Where domain", spec.guard)
	}
}
