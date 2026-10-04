package codegen

import (
	"strings"
	"testing"
)

func TestValidate_RawModelBindingDoesNotProvideCRUD(t *testing.T) {
	in := testInput([]Model{contactModel()}, Route{
		Method: "GET", Path: "/search", Model: "contacts.contact", CRUDAction: "list",
	})
	in.Views = []View{{Name: "contacts_list", Type: "list", Resource: "contacts.contact"}}

	err := Validate(in)
	if err == nil || !strings.Contains(err.Error(), `resource contacts.contact has no "list" op`) {
		t.Fatalf("Validate() error = %v, want a missing list op", err)
	}
}

func TestValidate_ReservedActionProvidesCRUD(t *testing.T) {
	in := testInput([]Model{contactModel()}, Route{
		Model: "contacts.contact", Name: "list", CRUDAction: "list", Scope: CollectionScope,
	})
	in.Views = []View{{Name: "contacts_list", Type: "list", Resource: "contacts.contact"}}

	if err := Validate(in); err != nil {
		t.Fatalf("Validate() error = %v, want a reserved action to provide list", err)
	}
}
