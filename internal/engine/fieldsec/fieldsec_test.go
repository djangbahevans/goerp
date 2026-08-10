package fieldsec

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestBuildFieldSecRegistry_EmptyModules_ReturnsEmptyRegistry(t *testing.T) {
	reg := buildFieldSecRegistry(map[string]*module.LoadedModule{})

	if _, ok := reg.Rule("contacts.contact", "ssn"); ok {
		t.Fatalf("expected no rule from an empty registry")
	}
}

func TestBuildFieldSecRegistry_ModuleWithModels_NoRulesYet(t *testing.T) {
	modules := map[string]*module.LoadedModule{
		"contacts": {
			ModelDecls: []model.ModelDeclaration{
				{
					Name: "contact",
					Fields: []model.NamedField{
						{Name: "ssn"},
						{Name: "name"},
					},
				},
			},
		},
	}

	reg := buildFieldSecRegistry(modules)

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

func TestBuildFieldSecRegistry_MultipleFieldsPerModel_AllRetained(t *testing.T) {
	// Regression test: an earlier version of buildFieldSecRegistry keyed its
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
