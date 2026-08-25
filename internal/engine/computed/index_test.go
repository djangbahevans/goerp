package computed

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func salesOrderDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "order",
		Fields: []model.NamedField{
			{Name: "id", Def: model.FieldDef{Kind: model.KindUUID, IsPrimaryKey: true}},
			{Name: "customer_id", Def: model.Many2One("contacts.contact")},
			{Name: "discount_pct", Def: model.Integer()},
			{
				Name: "amount_total",
				Def: model.BigInt().
					Computed("_compute_amount_total").
					Store(true).
					Depends("discount_pct", "customer.credit_limit"),
			},
		},
	}
}

func contactDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "contact",
		Fields: []model.NamedField{
			{Name: "id", Def: model.FieldDef{Kind: model.KindUUID, IsPrimaryKey: true}},
			{Name: "credit_limit", Def: model.Integer()},
		},
	}
}

func TestIndex_Lookup_SameRecordDependency(t *testing.T) {
	idx := New()
	idx.Register("sales", []model.ModelDeclaration{salesOrderDecl()})

	deps := idx.Lookup("sales.order", []string{"discount_pct"})
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependent, got %d: %+v", len(deps), deps)
	}
	got := deps[0]
	if got.Field != "amount_total" || got.ComputeFn != "_compute_amount_total" || got.ViaFKField != "" {
		t.Fatalf("unexpected dependent: %+v", got)
	}
}

func TestIndex_Lookup_Many2OneHopDependency(t *testing.T) {
	idx := New()
	idx.Register("sales", []model.ModelDeclaration{salesOrderDecl()})
	idx.Register("contacts", []model.ModelDeclaration{contactDecl()})

	// Writing contacts.contact.credit_limit should surface sales.order's
	// amount_total as a dependent, reached via the customer_id hop.
	deps := idx.Lookup("contacts.contact", []string{"credit_limit"})
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependent, got %d: %+v", len(deps), deps)
	}
	got := deps[0]
	if got.ModuleName != "sales" || got.ModelDecl.Name != "order" || got.Field != "amount_total" || got.ViaFKField != "customer_id" {
		t.Fatalf("unexpected dependent: %+v", got)
	}
}

func TestIndex_Lookup_NoMatch_ReturnsEmpty(t *testing.T) {
	idx := New()
	idx.Register("sales", []model.ModelDeclaration{salesOrderDecl()})

	if deps := idx.Lookup("sales.order", []string{"unrelated_field"}); len(deps) != 0 {
		t.Fatalf("expected no dependents, got %+v", deps)
	}
}

func TestIndex_Lookup_DedupesAcrossMultipleChangedFields(t *testing.T) {
	idx := New()
	idx.Register("sales", []model.ModelDeclaration{salesOrderDecl()})
	idx.Register("contacts", []model.ModelDeclaration{contactDecl()})

	deps := idx.Lookup("sales.order", []string{"discount_pct", "discount_pct"})
	if len(deps) != 1 {
		t.Fatalf("expected exactly 1 deduped dependent, got %d: %+v", len(deps), deps)
	}
}

// TestIndex_Register_UnresolvedHop_SkipsSilently covers a Depends() path
// through a relField that isn't a declared Many2One field on the same
// model — a malformed declaration this package has no load-time
// validation authority over (internal/engine/loader's job), so it should
// index nothing for that path rather than panicking.
func TestIndex_Register_UnresolvedHop_SkipsSilently(t *testing.T) {
	decl := model.ModelDeclaration{
		Name: "order",
		Fields: []model.NamedField{
			{
				Name: "amount_total",
				Def:  model.BigInt().Computed("_compute_amount_total").Store(true).Depends("nonexistent.some_field"),
			},
		},
	}

	idx := New()
	idx.Register("sales", []model.ModelDeclaration{decl})

	if deps := idx.Lookup("anything.anything", []string{"some_field"}); len(deps) != 0 {
		t.Fatalf("expected no dependents for an unresolved hop, got %+v", deps)
	}
}

func TestIndex_Register_NonComputedField_NeverIndexed(t *testing.T) {
	decl := model.ModelDeclaration{
		Name: "order",
		Fields: []model.NamedField{
			{Name: "discount_pct", Def: model.Integer()},
		},
	}

	idx := New()
	idx.Register("sales", []model.ModelDeclaration{decl})

	if deps := idx.Lookup("sales.order", []string{"discount_pct"}); len(deps) != 0 {
		t.Fatalf("expected no dependents for a non-computed field, got %+v", deps)
	}
}
