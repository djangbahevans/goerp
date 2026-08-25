package dataaudit

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func contactDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "contact",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
		},
	}
}

func orderDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "order",
		Table: "sales_order",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
		},
	}
}

func TestRegister_BareStringEntry(t *testing.T) {
	r := New()
	r.Register("contacts", []manifest.AuditedTable{{Table: "contact"}}, []model.ModelDeclaration{contactDecl()})

	excludeCols, audited := r.Lookup("contacts.contact")
	if !audited {
		t.Fatal("expected contacts.contact to be audited")
	}
	if len(excludeCols) != 0 {
		t.Errorf("expected no excluded columns, got %+v", excludeCols)
	}
}

func TestRegister_ObjectEntryWithExcludeColumns(t *testing.T) {
	r := New()
	r.Register("contacts", []manifest.AuditedTable{
		{Table: "contact", ExcludeColumns: []string{"etag", "updated_at"}},
	}, []model.ModelDeclaration{contactDecl()})

	excludeCols, audited := r.Lookup("contacts.contact")
	if !audited {
		t.Fatal("expected contacts.contact to be audited")
	}
	if !excludeCols["etag"] || !excludeCols["updated_at"] {
		t.Errorf("expected etag and updated_at excluded, got %+v", excludeCols)
	}
	if len(excludeCols) != 2 {
		t.Errorf("expected exactly 2 excluded columns, got %+v", excludeCols)
	}
}

func TestRegister_MatchesExplicitTableOverride(t *testing.T) {
	r := New()
	r.Register("sales", []manifest.AuditedTable{{Table: "sales_order"}}, []model.ModelDeclaration{orderDecl()})

	if _, audited := r.Lookup("sales.order"); !audited {
		t.Fatal("expected sales.order (table sales_order) to be audited")
	}
}

func TestRegister_BothFormsInSameArray(t *testing.T) {
	r := New()
	r.Register("contacts", []manifest.AuditedTable{
		{Table: "contact"},
		{Table: "sales_order", ExcludeColumns: []string{"etag"}},
	}, []model.ModelDeclaration{contactDecl(), orderDecl()})

	if _, audited := r.Lookup("contacts.contact"); !audited {
		t.Fatal("expected contacts.contact to be audited")
	}
	excludeCols, audited := r.Lookup("contacts.order")
	if !audited {
		t.Fatal("expected contacts.order to be audited")
	}
	if !excludeCols["etag"] {
		t.Errorf("expected etag excluded, got %+v", excludeCols)
	}
}

func TestRegister_UnmatchedTableName_SkippedSilently(t *testing.T) {
	r := New()
	r.Register("contacts", []manifest.AuditedTable{{Table: "nonexistent_table"}}, []model.ModelDeclaration{contactDecl()})

	if _, audited := r.Lookup("contacts.contact"); audited {
		t.Fatal("expected contacts.contact to remain unaudited")
	}
}

func TestLookup_UnauditedModel_ReturnsFalse(t *testing.T) {
	r := New()
	r.Register("contacts", []manifest.AuditedTable{{Table: "contact"}}, []model.ModelDeclaration{contactDecl(), orderDecl()})

	if _, audited := r.Lookup("contacts.order"); audited {
		t.Fatal("expected contacts.order to be unaudited")
	}
	if _, audited := r.Lookup("othermodule.unknown"); audited {
		t.Fatal("expected an entirely unregistered model to be unaudited")
	}
}
