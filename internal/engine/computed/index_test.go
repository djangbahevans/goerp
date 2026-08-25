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

// orderWithLinesDecl and orderLineDecl exercise the One2Many-hop case:
// writing/creating/unlinking an order_line row must recompute
// order_with_lines.lines_total, resolved via the "lines" One2Many field's
// inverse ("order_id" on order_line).
func orderWithLinesDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "order_with_lines",
		Fields: []model.NamedField{
			{Name: "id", Def: model.FieldDef{Kind: model.KindUUID, IsPrimaryKey: true}},
			{Name: "lines", Def: model.One2Many("sales.order_line", "order_id")},
			{
				Name: "lines_total",
				Def: model.BigInt().
					Computed("_compute_lines_total").
					Store(true).
					Depends("lines.quantity", "lines.unit_price"),
			},
		},
	}
}

func orderLineDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "order_line",
		Fields: []model.NamedField{
			{Name: "id", Def: model.FieldDef{Kind: model.KindUUID, IsPrimaryKey: true}},
			{Name: "order_id", Def: model.Many2One("sales.order_with_lines")},
			{Name: "quantity", Def: model.Integer()},
			{Name: "unit_price", Def: model.Integer()},
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

func TestIndex_LookupViaChild_One2ManyHopDependency(t *testing.T) {
	idx := New()
	idx.Register("sales", []model.ModelDeclaration{orderWithLinesDecl(), orderLineDecl()})

	// Writing/creating/unlinking sales.order_line.quantity should surface
	// order_with_lines' lines_total as a dependent, reached via the
	// "lines" One2Many field's inverse ("order_id" on order_line).
	deps := idx.LookupViaChild("sales.order_line", []string{"quantity"})
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependent, got %d: %+v", len(deps), deps)
	}
	got := deps[0]
	if got.ModuleName != "sales" || got.ModelDecl.Name != "order_with_lines" || got.Field != "lines_total" || got.ViaChildFKField != "order_id" {
		t.Fatalf("unexpected dependent: %+v", got)
	}
	if got.ViaFKField != "" {
		t.Fatalf("expected ViaFKField to stay empty for a One2Many hop, got %q", got.ViaFKField)
	}
}

func TestIndex_Lookup_AlsoIncludesOne2ManyHopDependency(t *testing.T) {
	idx := New()
	idx.Register("sales", []model.ModelDeclaration{orderWithLinesDecl(), orderLineDecl()})

	deps := idx.Lookup("sales.order_line", []string{"unit_price"})
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependent, got %d: %+v", len(deps), deps)
	}
	if deps[0].Field != "lines_total" || deps[0].ViaChildFKField != "order_id" {
		t.Fatalf("unexpected dependent: %+v", deps[0])
	}
}

func TestIndex_LookupViaChild_NoMatch_ReturnsEmpty(t *testing.T) {
	idx := New()
	idx.Register("sales", []model.ModelDeclaration{orderWithLinesDecl(), orderLineDecl()})

	if deps := idx.LookupViaChild("sales.order_line", []string{"unrelated_field"}); len(deps) != 0 {
		t.Fatalf("expected no dependents, got %+v", deps)
	}
}

func TestIndex_LookupViaChild_DedupesAcrossMultipleChangedFields(t *testing.T) {
	idx := New()
	idx.Register("sales", []model.ModelDeclaration{orderWithLinesDecl(), orderLineDecl()})

	deps := idx.LookupViaChild("sales.order_line", []string{"quantity", "unit_price"})
	if len(deps) != 1 {
		t.Fatalf("expected exactly 1 deduped dependent, got %d: %+v", len(deps), deps)
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
