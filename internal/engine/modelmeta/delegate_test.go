package modelmeta

import (
	"slices"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestComposedFields(t *testing.T) {
	contact := *model.Define("contacts.contact").WithStandardFields().Field("name", model.Text()).Field("phone", model.Text())
	employee := *model.Define("hr.employee").WithStandardFields().Field("contact_id", model.Delegate("contacts.contact")).Field("name", model.Text())
	lookup := func(name string) (model.ModelDeclaration, bool) { return contact, name == "contacts.contact" }
	fields, err := ComposedFields(employee, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != len(employee.Fields)+1 || !slices.ContainsFunc(fields, func(f model.NamedField) bool { return f.Name == "phone" }) {
		t.Fatalf("own fields must shadow target fields: %+v", fields)
	}

	contact.Fields = append(contact.Fields, model.NamedField{Name: "employee_id", Def: model.Delegate("hr.employee")})
	_, err = ComposedFields(employee, func(name string) (model.ModelDeclaration, bool) {
		if name == "contacts.contact" {
			return contact, true
		}
		return employee, name == "hr.employee"
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("delegation cycle accepted: %v", err)
	}
}

func TestComposedFieldsRejectsInvalidDelegates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		field  model.NamedField
		target model.ModelDeclaration
	}{
		{name: "suffix", field: model.NamedField{Name: "contact", Def: model.Delegate("contacts.contact")}},
		{name: "empty stem", field: model.NamedField{Name: "_id", Def: model.Delegate("contacts.contact")}},
		{name: "tree", field: model.NamedField{Name: "contact_id", Def: model.Delegate("contacts.contact").Tree()}},
		{name: "target backend", field: model.NamedField{Name: "contact_id", Def: model.Delegate("contacts.contact")}, target: model.ModelDeclaration{Backend: model.BackendVirtual}},
		{name: "target identity", field: model.NamedField{Name: "contact_id", Def: model.Delegate("contacts.contact")}, target: model.ModelDeclaration{Fields: []model.NamedField{{Name: "id", Def: model.Text().PrimaryKey()}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ComposedFields(model.ModelDeclaration{Fields: []model.NamedField{tc.field}}, func(string) (model.ModelDeclaration, bool) { return tc.target, true })
			if err == nil {
				t.Fatal("invalid Delegate declaration accepted")
			}
		})
	}
}
