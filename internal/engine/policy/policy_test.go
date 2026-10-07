package policy

import (
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func orderModel() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "order",
		Table: "sales_orders",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "owner_id", Def: model.UUID()},
			{Name: "state", Def: model.Text()},
		},
	}
}

func TestRegister_ResolvesTableKeyAndColumns(t *testing.T) {
	reg := New()
	reg.Register([]manifest.Policy{{
		Name:      "sales:order:own_open",
		AppliesTo: "sales:order:read",
		Condition: "record.owner_id = current_user.id AND record.state != 'void'",
	}}, map[string][]model.ModelDeclaration{"sales": {orderModel()}})

	got := reg.For("sales:order:read")
	if len(got) != 1 {
		t.Fatalf("For = %d policies, want 1", len(got))
	}
	p := got[0]
	if p.Name != "sales:order:own_open" || p.Table != "sales_orders" || p.PKColumn != "id" {
		t.Errorf("policy = %+v, want the table and key of sales.order", p)
	}
	if want := []string{"owner_id", "state"}; !slices.Equal(p.Columns, want) {
		t.Errorf("Columns = %v, want %v", p.Columns, want)
	}
}

// A policy declared by one module can scope another module's permission, so
// the model comes from the permission's owner, not the declaring module.
func TestRegister_PolicyScopingAnotherModulesPermission(t *testing.T) {
	reg := New()
	reg.Register([]manifest.Policy{{
		Name:      "reports:order:regional",
		AppliesTo: "sales:order:read",
		Condition: "record.state = 'open'",
	}}, map[string][]model.ModelDeclaration{"sales": {orderModel()}, "reports": nil})

	got := reg.For("sales:order:read")
	if len(got) != 1 || got[0].Table != "sales_orders" {
		t.Fatalf("For = %+v, want one policy over sales_orders", got)
	}
}

// An unresolvable policy stays registered, marked Unresolved, so a check that
// depends on it fails rather than losing the policy and answering role-only.
func TestRegister_KeepsPoliciesItCannotResolve(t *testing.T) {
	noKey := model.ModelDeclaration{Name: "keyless", Fields: []model.NamedField{{Name: "name", Def: model.Text()}}}
	models := map[string][]model.ModelDeclaration{"sales": {orderModel(), noKey}}
	reg := New()
	reg.Register([]manifest.Policy{
		{Name: "a", AppliesTo: "ghost:order:read", Condition: "true"},
		{Name: "b", AppliesTo: "sales:invoice:read", Condition: "true"},
		{Name: "c", AppliesTo: "sales:order:read", Condition: "record.state ="},
		{Name: "d", AppliesTo: "not-a-permission", Condition: "true"},
		{Name: "e", AppliesTo: "sales:keyless:read", Condition: "true"},
	}, models)

	for _, perm := range []string{"ghost:order:read", "sales:invoice:read", "sales:order:read", "not-a-permission", "sales:keyless:read"} {
		got := reg.For(perm)
		if len(got) != 1 || got[0].Unresolved == nil {
			t.Errorf("For(%q) = %+v, want one policy marked Unresolved", perm, got)
		}
	}
}

func TestFor_NilRegistryHasNoPolicies(t *testing.T) {
	var reg *Registry
	if got := reg.For("sales:order:read"); got != nil {
		t.Errorf("For on a nil registry = %+v, want nil", got)
	}
}

func TestFor_ReturnsEveryPolicyOfAPermission(t *testing.T) {
	reg := New()
	reg.Register([]manifest.Policy{
		{Name: "one", AppliesTo: "sales:order:read", Condition: "true"},
		{Name: "two", AppliesTo: "sales:order:read", Condition: "false"},
		{Name: "other", AppliesTo: "sales:order:write", Condition: "true"},
	}, map[string][]model.ModelDeclaration{"sales": {orderModel()}})

	if got := reg.For("sales:order:read"); len(got) != 2 {
		t.Errorf("For(read) = %d policies, want 2", len(got))
	}
	if got := reg.For("sales:order:write"); len(got) != 1 {
		t.Errorf("For(write) = %d policies, want 1", len(got))
	}
}
