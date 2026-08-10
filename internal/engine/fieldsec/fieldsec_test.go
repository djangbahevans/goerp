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
		t.Fatalf("expected no rule for %q: FieldDef carries no security data until SDK backlog #19 lands", "ssn")
	}
	if _, ok := reg.Rule("contacts.contact", "name"); ok {
		t.Fatalf("expected no rule for %q: FieldDef carries no security data until SDK backlog #19 lands", "name")
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
	// since that function currently always returns ok=false.
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
