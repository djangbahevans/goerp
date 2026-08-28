package searchindex

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func TestRegister_QualifiesByModule(t *testing.T) {
	r := New()
	r.Register("contacts", []manifest.SearchIndex{
		{Name: "contacts", Table: "contact", Searchable: []string{"name"}, Displayed: []string{"id", "name"}},
	})

	idx, ok := r.Index("contacts", "contacts")
	if !ok {
		t.Fatal("expected contacts.contacts to be declared")
	}
	if idx.Table != "contact" {
		t.Errorf("Table = %q, want %q", idx.Table, "contact")
	}
}

func TestIndex_UnknownReturnsFalse(t *testing.T) {
	r := New()
	if _, ok := r.Index("contacts", "nonexistent"); ok {
		t.Error("expected no rule for an undeclared index")
	}
}

func TestRegister_SameBareNameDifferentModulesDontCollide(t *testing.T) {
	r := New()
	r.Register("contacts", []manifest.SearchIndex{{Name: "items", Table: "contact"}})
	r.Register("sales", []manifest.SearchIndex{{Name: "items", Table: "sales_order"}})

	contactsIdx, ok := r.Index("contacts", "items")
	if !ok || contactsIdx.Table != "contact" {
		t.Errorf("contacts.items = (%+v, %v), want Table \"contact\"", contactsIdx, ok)
	}
	salesIdx, ok := r.Index("sales", "items")
	if !ok || salesIdx.Table != "sales_order" {
		t.Errorf("sales.items = (%+v, %v), want Table \"sales_order\"", salesIdx, ok)
	}
}
