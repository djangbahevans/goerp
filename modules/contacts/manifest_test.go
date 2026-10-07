package contacts_test

import (
	"encoding/json/v2"
	"os"
	"slices"
	"testing"
)

type manifestView struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Resource string `json:"resource"`
	RowClick string `json:"row_click"`
	Actions  []struct {
		Type string `json:"type"`
		View string `json:"view"`
	} `json:"actions"`
}

type manifestNavItem struct {
	Label      string `json:"label"`
	View       string `json:"view"`
	Route      string `json:"route"`
	Permission string `json:"permission"`
}

type manifestFile struct {
	Views      []manifestView `json:"views"`
	Navigation []struct {
		Label    string            `json:"label"`
		Order    int               `json:"order"`
		Children []manifestNavItem `json:"children"`
	} `json:"navigation"`
	ViewExtensions           []any `json:"view_extensions"`
	ViewExtensionDefinitions []any `json:"view_extension_definitions"`
}

func loadManifest(t *testing.T) manifestFile {
	t.Helper()
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var m manifestFile
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest.json: %v", err)
	}
	return m
}

func TestManifestViewsReferenceOnlyContactsViews(t *testing.T) {
	m := loadManifest(t)

	names := make([]string, 0, len(m.Views))
	for _, v := range m.Views {
		names = append(names, v.Name)
		if v.Resource != "contacts.contact" {
			t.Errorf("view %q resource = %q, want contacts.contact", v.Name, v.Resource)
		}
	}
	if want := []string{"contacts_list", "contacts_form"}; !slices.Equal(names, want) {
		t.Fatalf("view names = %v, want %v", names, want)
	}

	list := m.Views[0]
	if list.Type != "list" || list.RowClick != "contacts_form" {
		t.Errorf("contacts_list type = %q, row_click = %q, want list and contacts_form", list.Type, list.RowClick)
	}
	if len(list.Actions) != 1 || list.Actions[0].Type != "create" || list.Actions[0].View != "contacts_form" {
		t.Errorf("contacts_list actions = %+v, want one create action targeting contacts_form", list.Actions)
	}
	if got := m.Views[1].Type; got != "form" {
		t.Errorf("contacts_form type = %q, want form", got)
	}

	if len(m.ViewExtensions) != 0 || len(m.ViewExtensionDefinitions) != 0 {
		t.Errorf("manifest declares view extensions; consuming modules own those")
	}
}

func TestManifestNavigation(t *testing.T) {
	m := loadManifest(t)

	if len(m.Navigation) != 1 {
		t.Fatalf("navigation groups = %d, want 1", len(m.Navigation))
	}
	group := m.Navigation[0]
	if group.Label != "Contacts" || group.Order != 10 {
		t.Errorf("group = %q order %d, want Contacts order 10", group.Label, group.Order)
	}

	want := []manifestNavItem{
		{"All Contacts", "contacts_list", "/contacts", "contacts:contact:read"},
		{"Customers", "contacts_list", "/contacts?filter[is_customer]=true&filter[is_active]=true", "contacts:contact:read"},
		{"Suppliers", "contacts_list", "/contacts?filter[is_supplier]=true&filter[is_active]=true", "contacts:contact:read"},
	}
	if !slices.Equal(group.Children, want) {
		t.Errorf("children = %+v, want %+v", group.Children, want)
	}
}
