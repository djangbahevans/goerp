package contacts_test

import (
	"encoding/json/v2"
	"os"
	"reflect"
	"slices"
	"testing"
)

type manifestView struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Resource string `json:"resource"`
	RowClick string `json:"row_click"`
	Columns  []struct {
		Field  string `json:"field"`
		Hidden bool   `json:"hidden"`
	} `json:"columns"`
	Tabs []struct {
		Label            string            `json:"label"`
		Type             string            `json:"type"`
		View             string            `json:"view"`
		Filter           map[string]string `json:"filter"`
		ShowCreateAction bool              `json:"show_create_action"`
	} `json:"tabs"`
	Sections []struct {
		Name string `json:"name"`
	} `json:"sections"`
	Actions []struct {
		Type string `json:"type"`
		View string `json:"view"`
	} `json:"actions"`
}

type manifestNavItem struct {
	Label          string         `json:"label"`
	View           string         `json:"view"`
	Route          string         `json:"route"`
	DefaultFilters map[string]any `json:"default_filters"`
	Permission     string         `json:"permission"`
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
		if v.Resource != "contacts.contact" && v.Resource != "contacts.tag" && v.Resource != "contacts.address" && v.Resource != "contacts.identifier_type" && v.Resource != "contacts.affiliation" {
			t.Errorf("view %q resource = %q, want a Contacts resource", v.Name, v.Resource)
		}
	}
	if want := []string{"contacts_list", "contacts_form", "tags_list", "tags_form", "addresses_for_contact", "address_form", "identifier_types_list", "identifier_type_form", "affiliations_for_organization", "affiliation_form"}; !slices.Equal(names, want) {
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

func TestManifestSortColumn(t *testing.T) {
	var hidden []string
	for _, c := range loadManifest(t).Views[0].Columns {
		if c.Hidden {
			hidden = append(hidden, c.Field)
		}
	}
	if !slices.Equal(hidden, []string{"sort_name"}) {
		t.Errorf("hidden columns = %v", hidden)
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
		{"All Contacts", "contacts_list", "/contacts", nil, "contacts:contact:read"},
		{"People", "contacts_list", "/contacts", map[string]any{"type": "person", "is_active": true}, "contacts:contact:read"},
		{"Organizations", "contacts_list", "/contacts", map[string]any{"type": "organization", "is_active": true}, "contacts:contact:read"},
		{"Tags", "tags_list", "/tags", nil, "contacts:config:write"},
		{"Identifier Types", "identifier_types_list", "/identifier-types", nil, "contacts:config:write"},
	}
	if !reflect.DeepEqual(group.Children, want) {
		t.Errorf("children = %+v, want %+v", group.Children, want)
	}
}

func TestAddressViewsExposeExtensionTargets(t *testing.T) {
	m := loadManifest(t)
	var list, form *manifestView
	for i := range m.Views {
		view := &m.Views[i]
		switch view.Name {
		case "addresses_for_contact":
			list = view
		case "address_form":
			form = view
		}
	}
	if list == nil || form == nil {
		t.Fatal("named address list and form are required")
	}
	if list.Resource != "contacts.address" || list.RowClick != "address_form" || len(list.Columns) == 0 ||
		len(list.Actions) != 1 || list.Actions[0].Type != "create" || list.Actions[0].View != "address_form" {
		t.Errorf("address list = %+v", list)
	}
	if form.Resource != "contacts.address" || len(form.Sections) != 1 || form.Sections[0].Name != "address" {
		t.Errorf("address form = %+v", form)
	}

	for _, tab := range m.Views[1].Tabs {
		if tab.Label == "Addresses" {
			if tab.Type != "view" || tab.View != "contacts.addresses_for_contact" || !tab.ShowCreateAction || tab.Filter["contact_id"] != "record.id" {
				t.Errorf("Addresses tab = %+v", tab)
			}
			return
		}
	}
	t.Fatal("contact form requires an Addresses view tab")
}
