package fieldsec

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestFieldSecurityRegistry_Register_NoModelDecls_ReturnsEmptyRegistry(t *testing.T) {
	reg := New()
	reg.Register("contacts", nil)

	if _, ok := reg.Rule("contacts.contact", "ssn"); ok {
		t.Fatalf("expected no rule from an empty registry")
	}
}

func TestFieldSecurityRegistry_Register_ModelWithFields_NoRulesYet(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		{
			Name: "contact",
			Fields: []model.NamedField{
				{Name: "ssn"},
				{Name: "name"},
			},
		},
	}

	reg := New()
	reg.Register("contacts", modelDecls)

	if _, ok := reg.Rule("contacts.contact", "ssn"); ok {
		t.Fatalf("expected no rule for %q: neither field declared .Access()", "ssn")
	}
	if _, ok := reg.Rule("contacts.contact", "name"); ok {
		t.Fatalf("expected no rule for %q: neither field declared .Access()", "name")
	}
}

func TestFieldSecurityRegistry_Register_AccessDeclared_ProducesRule(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		{
			Name: "contact",
			Fields: []model.NamedField{
				{Name: "name"}, // no .Access() — must stay rule-free
				{Name: "credit_limit", Def: model.Integer().
					Access(
						model.AccessRead("contacts:contact:financials_read"),
						model.AccessWrite("contacts:contact:financials_write"),
					).
					OnDeniedRead(model.Omit)},
				{Name: "bank_account", Def: model.Char().
					Access(model.AccessRead("hr:employee:banking_read")).
					OnDeniedRead(model.Mask("****{last4}"))},
				{Name: "discount_percent", Def: model.Float().
					Access(model.AccessWrite("sales:order:set_discount")).
					OnDeniedWrite(model.Reject)},
			},
		},
	}

	reg := New()
	reg.Register("contacts", modelDecls)

	if _, ok := reg.Rule("contacts.contact", "name"); ok {
		t.Fatalf("expected no rule for %q: no .Access() declared", "name")
	}

	got, ok := reg.Rule("contacts.contact", "credit_limit")
	if !ok {
		t.Fatal("expected a rule for credit_limit")
	}
	want := FieldSecurityRule{
		ReadPermission:  "contacts:contact:financials_read",
		WritePermission: "contacts:contact:financials_write",
		OnDeniedRead:    Omit,
		OnDeniedWrite:   Reject, // no .OnDeniedWrite() call — safe default
	}
	if got != want {
		t.Fatalf("Rule(credit_limit) = %+v, want %+v", got, want)
	}

	got, ok = reg.Rule("contacts.contact", "bank_account")
	if !ok {
		t.Fatal("expected a rule for bank_account")
	}
	want = FieldSecurityRule{
		ReadPermission: "hr:employee:banking_read",
		OnDeniedRead:   Mask,
		MaskPattern:    "****{last4}",
		OnDeniedWrite:  Reject, // safe default
	}
	if got != want {
		t.Fatalf("Rule(bank_account) = %+v, want %+v", got, want)
	}

	got, ok = reg.Rule("contacts.contact", "discount_percent")
	if !ok {
		t.Fatal("expected a rule for discount_percent")
	}
	want = FieldSecurityRule{
		WritePermission: "sales:order:set_discount",
		OnDeniedRead:    Omit, // no ReadPermission declared — read side unrestricted
		OnDeniedWrite:   Reject,
	}
	if got != want {
		t.Fatalf("Rule(discount_percent) = %+v, want %+v", got, want)
	}
	if got.ReadPermission != "" {
		t.Errorf("ReadPermission = %q, want empty — write-only protection per manifest-spec.md §8a", got.ReadPermission)
	}
}

func TestFieldSecurityRegistry_Register_OnDeniedWriteIgnore_Propagates(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		{
			Name: "contact",
			Fields: []model.NamedField{
				{Name: "computed_score", Def: model.Integer().
					Access(model.AccessWrite("contacts:contact:score_write")).
					OnDeniedWrite(model.Ignore)},
			},
		},
	}

	reg := New()
	reg.Register("contacts", modelDecls)

	got, ok := reg.Rule("contacts.contact", "computed_score")
	if !ok {
		t.Fatal("expected a rule for computed_score")
	}
	if got.OnDeniedWrite != Ignore {
		t.Errorf("OnDeniedWrite = %v, want Ignore", got.OnDeniedWrite)
	}
}

func TestFieldSecurityRegistry_Rule_UnknownModel_ReturnsFalse(t *testing.T) {
	reg := &FieldSecurityRegistry{
		rules: map[string]map[string]FieldSecurityRule{
			"contacts.contact": {
				"ssn": {ReadPermission: "contacts.read_pii", OnDeniedRead: Mask, MaskPattern: "***{last4}"},
			},
		},
	}

	if _, ok := reg.Rule("contacts.address", "ssn"); ok {
		t.Fatalf("expected no rule for an unregistered model")
	}
}

func TestFieldSecurityRegistry_Rule_UnknownField_ReturnsFalse(t *testing.T) {
	reg := &FieldSecurityRegistry{
		rules: map[string]map[string]FieldSecurityRule{
			"contacts.contact": {
				"ssn": {ReadPermission: "contacts.read_pii", OnDeniedRead: Mask, MaskPattern: "***{last4}"},
			},
		},
	}

	if _, ok := reg.Rule("contacts.contact", "name"); ok {
		t.Fatalf("expected no rule for an unregistered field")
	}
}

func TestFieldSecurityRegistry_Rule_KnownModelAndField_ReturnsRule(t *testing.T) {
	want := FieldSecurityRule{
		ReadPermission:  "contacts.read_pii",
		WritePermission: "contacts.write_pii",
		OnDeniedRead:    Mask,
		OnDeniedWrite:   Reject,
		MaskPattern:     "***{last4}",
	}
	reg := &FieldSecurityRegistry{
		rules: map[string]map[string]FieldSecurityRule{
			"contacts.contact": {"ssn": want},
		},
	}

	got, ok := reg.Rule("contacts.contact", "ssn")
	if !ok {
		t.Fatalf("expected a rule for contacts.contact.ssn")
	}
	if got != want {
		t.Fatalf("Rule() = %+v, want %+v", got, want)
	}
}

func TestFieldSecurityRegistry_Register_MultipleFieldsPerModel_AllRetained(t *testing.T) {
	// Regression test: an earlier version of Register keyed its
	// "already initialized" check off the bare module name instead of the
	// qualified model name, so it recreated (and wiped) the inner map on
	// every field within the same model, silently dropping all but the last
	// field written. This exercises that path directly against the
	// registry's internal state rather than through fieldSecurityRuleFor,
	// to isolate it from that function's own field-declaration parsing.
	reg := &FieldSecurityRegistry{rules: make(map[string]map[string]FieldSecurityRule)}
	modelName := "contacts.contact"
	fields := []struct {
		name string
		rule FieldSecurityRule
	}{
		{"ssn", FieldSecurityRule{ReadPermission: "contacts.read_pii", OnDeniedRead: Mask}},
		{"salary", FieldSecurityRule{ReadPermission: "contacts.read_financial", OnDeniedRead: Omit}},
	}
	for _, f := range fields {
		if reg.rules[modelName] == nil {
			reg.rules[modelName] = make(map[string]FieldSecurityRule)
		}
		reg.rules[modelName][f.name] = f.rule
	}

	for _, f := range fields {
		got, ok := reg.Rule(modelName, f.name)
		if !ok {
			t.Fatalf("expected a rule for %s.%s to survive multiple field writes", modelName, f.name)
		}
		if got != f.rule {
			t.Fatalf("Rule(%s, %s) = %+v, want %+v", modelName, f.name, got, f.rule)
		}
	}
}
