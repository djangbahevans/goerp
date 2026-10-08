package model

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/vmihailenco/msgpack/v5"
)

func TestOpRequiresPreservesConditionAndRoundTrips(t *testing.T) {
	permission := perm.Ref("sales:order:read")
	condition := "record.owner_id = current_user.id"

	for _, op := range []Op{
		List.Requires(permission).WithCondition(condition),
		List.WithCondition(condition).Requires(permission),
	} {
		original := Define("sales.order").EnableOps(op, Get)
		data, err := msgpack.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}

		var decoded ModelDeclaration
		if err := msgpack.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}

		if got := decoded.EnabledOps[0]; got.Name != "list" || got.Permission != permission.Name() || got.Condition != condition {
			t.Errorf("decoded op = %+v", got)
		}
		if got := decoded.EnabledOps[1].Permission; got != "" {
			t.Errorf("unrestricted Get permission = %q", got)
		}
	}

	if List.Permission != "" || List.Condition != "" {
		t.Fatalf("package List mutated: %+v", List)
	}
}

func TestOpRequiresRejectsZeroPermission(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Requires accepted a zero permission")
		}
	}()

	List.Requires(perm.Permission{})
}
