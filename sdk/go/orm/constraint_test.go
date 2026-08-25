package orm

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

func dispatchConstraintAndDecode(t *testing.T, req constraintRequest) constraintResponse {
	t.Helper()
	data, err := msgpack.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)

	packed := DispatchConstraint(ptr, uint32(len(data)))
	respPtr, respLen := uint32(packed>>32), uint32(packed)

	var resp constraintResponse
	if err := msgpack.Unmarshal(engine.ReadMem(respPtr, respLen), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestDispatchConstraint_RegisteredHook_Rejects(t *testing.T) {
	RegisterConstraint("test.order", OnDelete, func(ctx ConstraintContext, record map[string]any) *ConstraintResult {
		if record["state"] != "draft" {
			return Reject("state", "only draft orders can be deleted")
		}
		return Allow()
	})

	resp := dispatchConstraintAndDecode(t, constraintRequest{
		Model:  "test.order",
		Phase:  string(OnDelete),
		Record: map[string]any{"state": "confirmed"},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Allowed {
		t.Fatal("Allowed = true, want false")
	}
	if resp.Field != "state" || resp.Message != "only draft orders can be deleted" {
		t.Errorf("Field/Message = %q/%q, want state/\"only draft orders can be deleted\"", resp.Field, resp.Message)
	}
}

func TestDispatchConstraint_RegisteredHook_Allows(t *testing.T) {
	RegisterConstraint("test.order", OnDelete, func(ctx ConstraintContext, record map[string]any) *ConstraintResult {
		if record["state"] != "draft" {
			return Reject("state", "only draft orders can be deleted")
		}
		return Allow()
	})

	resp := dispatchConstraintAndDecode(t, constraintRequest{
		Model:  "test.order",
		Phase:  string(OnDelete),
		Record: map[string]any{"state": "draft"},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if !resp.Allowed {
		t.Fatal("Allowed = false, want true")
	}
}

// TestDispatchConstraint_UnregisteredPhase_PassesThroughAllowed covers the
// common case: a (model, phase) with no registered hook is a pass, not an
// error — the same reasoning orm.DispatchPreview uses for an
// unregistered model.
func TestDispatchConstraint_UnregisteredPhase_PassesThroughAllowed(t *testing.T) {
	RegisterConstraint("test.order", OnDelete, func(ctx ConstraintContext, record map[string]any) *ConstraintResult {
		return Reject("state", "never allowed")
	})

	// Same model, but OnCreate has no registered hook.
	resp := dispatchConstraintAndDecode(t, constraintRequest{
		Model: "test.order",
		Phase: string(OnCreate),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if !resp.Allowed {
		t.Fatal("Allowed = false, want true (no OnCreate hook registered for this model)")
	}
}

func TestDispatchConstraint_UnregisteredModel_PassesThroughAllowed(t *testing.T) {
	resp := dispatchConstraintAndDecode(t, constraintRequest{Model: "test.never_registered", Phase: string(OnWrite)})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if !resp.Allowed {
		t.Fatal("Allowed = false, want true")
	}
}
