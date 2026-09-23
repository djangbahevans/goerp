package orm

import (
	"reflect"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

type stockUnits int32

func TestMutateOptions_BuildOpsAndGuard(t *testing.T) {
	reserved := NewField[testModel, int64]("reserved")
	onHand := NewOrderedField[testModel, int64]("on_hand")
	weight := NewField[testModel, float64]("weight")
	units := NewField[testModel, stockUnits]("units")

	var spec mutateSpec
	for _, opt := range []MutateOption[testModel]{
		Increment(reserved, int64(4)),
		Decrement(onHand.Field, int64(4)),
		Decrement(weight, 0.5),
		Increment(units, stockUnits(2)),
		Where(onHand.Gte(4)),
	} {
		opt(&spec)
	}

	wantOps := []abi.ORMMutateOp{
		{Field: "reserved", Delta: int64(4)},
		{Field: "on_hand", Delta: int64(-4)},
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
