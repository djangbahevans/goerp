package loader

import (
	"context"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

// contactsListView is a minimal, well-formed "list" view a hr-style
// extending module can target for a "columns" extension.
func contactsListView() map[string]any {
	return map[string]any{
		"name":     "contacts_list",
		"type":     "list",
		"resource": "contacts.contact",
		"label":    "Contacts",
		"columns":  []map[string]any{{"field": "display_name"}},
		"filters":  []map[string]any{},
	}
}

// contactsFormView is a minimal, well-formed "form" view with one regular
// section (for a "fields" extension) and one sub_list section (for the
// fields-into-sub_list negative test).
func contactsFormView() map[string]any {
	return map[string]any{
		"name":     "contacts_form",
		"type":     "form",
		"resource": "contacts.contact",
		"label":    "Contact",
		"sections": []map[string]any{
			{"name": "details", "type": "fields", "fields": []map[string]any{{"field": "display_name"}}},
			{"name": "addresses", "type": "sub_list", "field": "address_ids"},
		},
		"tabs": []map[string]any{},
	}
}

func columnsExtensionDef(name string) map[string]any {
	return map[string]any{
		"name":           name,
		"type":           "columns",
		"target_section": "columns",
		"position":       "append",
		"columns":        []map[string]any{{"field": "hr:employee_id"}},
	}
}

func fieldsExtensionDef(name, targetSection string) map[string]any {
	return map[string]any{
		"name":           name,
		"type":           "fields",
		"target_section": targetSection,
		"position":       "append",
		"fields":         []map[string]any{{"field": "hr:job_title"}},
	}
}

func TestLoadAll_ValidViewExtensionSet_Loads(t *testing.T) {
	rt := newTestRuntime(t)
	sources := []Source{
		{
			Name: "contacts",
			ManifestBytes: manifestJSONWithFields(t, "contacts", okModule, []string{"db.read"}, map[string]any{
				"views": []map[string]any{contactsListView()},
			}),
			WasmBytes: okModule,
		},
		{
			Name: "hr",
			ManifestBytes: manifestJSONWithFields(t, "hr", okModule, []string{"db.read"}, map[string]any{
				"depends_on":                 []string{"contacts"},
				"view_extensions":            []map[string]any{{"extends": "contacts.contacts_list", "extension": "hr_employee_column"}},
				"view_extension_definitions": []map[string]any{columnsExtensionDef("hr_employee_column")},
			}),
			WasmBytes: okModule,
		},
	}

	modules := LoadAll(context.Background(), rt, testPoolCfg(), sources)

	if hr := modules["hr"]; hr.Status != module.StatusSyncing {
		t.Fatalf("hr.Status = %v, want StatusSyncing; FailureReason = %q", hr.Status, hr.FailureReason)
	}
}

func TestLoadAll_ViewExtensionSoftDependencyNotLoaded_LoadsWithoutError(t *testing.T) {
	rt := newTestRuntime(t)
	sources := []Source{
		{
			Name: "hr",
			ManifestBytes: manifestJSONWithFields(t, "hr", okModule, []string{"db.read"}, map[string]any{
				"soft_depends_on":            []string{"contacts"}, // contacts is never loaded
				"view_extensions":            []map[string]any{{"extends": "contacts.contacts_list", "extension": "hr_employee_column"}},
				"view_extension_definitions": []map[string]any{columnsExtensionDef("hr_employee_column")},
			}),
			WasmBytes: okModule,
		},
	}

	modules := LoadAll(context.Background(), rt, testPoolCfg(), sources)

	if hr := modules["hr"]; hr.Status != module.StatusSyncing {
		t.Fatalf("hr.Status = %v, want StatusSyncing (soft dependency absent should load silently); FailureReason = %q", hr.Status, hr.FailureReason)
	}
}

func TestLoadAll_ViewExtensionTargetViewMissing_Fails(t *testing.T) {
	rt := newTestRuntime(t)
	sources := []Source{
		{
			Name:          "contacts",
			ManifestBytes: manifestJSON(t, "contacts", okModule, []string{"db.read"}), // no views at all
			WasmBytes:     okModule,
		},
		{
			Name: "hr",
			ManifestBytes: manifestJSONWithFields(t, "hr", okModule, []string{"db.read"}, map[string]any{
				"depends_on":                 []string{"contacts"},
				"view_extensions":            []map[string]any{{"extends": "contacts.contacts_list", "extension": "hr_employee_column"}},
				"view_extension_definitions": []map[string]any{columnsExtensionDef("hr_employee_column")},
			}),
			WasmBytes: okModule,
		},
	}

	modules := LoadAll(context.Background(), rt, testPoolCfg(), sources)

	hr := modules["hr"]
	if hr.Status != module.StatusFailed {
		t.Fatalf("hr.Status = %v, want StatusFailed", hr.Status)
	}
	if !strings.Contains(hr.FailureReason, "no view named") {
		t.Errorf("hr.FailureReason = %q, want it to mention the missing view", hr.FailureReason)
	}
}

func TestLoadAll_FieldsExtensionTargetsSubList_Fails(t *testing.T) {
	rt := newTestRuntime(t)
	sources := []Source{
		{
			Name: "contacts",
			ManifestBytes: manifestJSONWithFields(t, "contacts", okModule, []string{"db.read"}, map[string]any{
				"views": []map[string]any{contactsFormView()},
			}),
			WasmBytes: okModule,
		},
		{
			Name: "hr",
			ManifestBytes: manifestJSONWithFields(t, "hr", okModule, []string{"db.read"}, map[string]any{
				"depends_on":                 []string{"contacts"},
				"view_extensions":            []map[string]any{{"extends": "contacts.contacts_form", "extension": "hr_address_field"}},
				"view_extension_definitions": []map[string]any{fieldsExtensionDef("hr_address_field", "addresses")},
			}),
			WasmBytes: okModule,
		},
	}

	modules := LoadAll(context.Background(), rt, testPoolCfg(), sources)

	hr := modules["hr"]
	if hr.Status != module.StatusFailed {
		t.Fatalf("hr.Status = %v, want StatusFailed", hr.Status)
	}
	if !strings.Contains(hr.FailureReason, "sub_list") {
		t.Errorf("hr.FailureReason = %q, want it to mention sub_list", hr.FailureReason)
	}
}

func TestLoadAll_ViewExtensionTargetSectionMissing_WarnsAndLoads(t *testing.T) {
	rt := newTestRuntime(t)
	sources := []Source{
		{
			Name: "contacts",
			ManifestBytes: manifestJSONWithFields(t, "contacts", okModule, []string{"db.read"}, map[string]any{
				"views": []map[string]any{contactsFormView()},
			}),
			WasmBytes: okModule,
		},
		{
			Name: "hr",
			ManifestBytes: manifestJSONWithFields(t, "hr", okModule, []string{"db.read"}, map[string]any{
				"depends_on":                 []string{"contacts"},
				"view_extensions":            []map[string]any{{"extends": "contacts.contacts_form", "extension": "hr_address_field"}},
				"view_extension_definitions": []map[string]any{fieldsExtensionDef("hr_address_field", "does_not_exist")},
			}),
			WasmBytes: okModule,
		},
	}

	modules := LoadAll(context.Background(), rt, testPoolCfg(), sources)

	if hr := modules["hr"]; hr.Status != module.StatusSyncing {
		t.Fatalf("hr.Status = %v, want StatusSyncing (missing target_section should warn, not fail); FailureReason = %q", hr.Status, hr.FailureReason)
	}
}

func TestLoadAll_ValidFieldsExtensionIntoRegularSection_Loads(t *testing.T) {
	rt := newTestRuntime(t)
	sources := []Source{
		{
			Name: "contacts",
			ManifestBytes: manifestJSONWithFields(t, "contacts", okModule, []string{"db.read"}, map[string]any{
				"views": []map[string]any{contactsFormView()},
			}),
			WasmBytes: okModule,
		},
		{
			Name: "hr",
			ManifestBytes: manifestJSONWithFields(t, "hr", okModule, []string{"db.read"}, map[string]any{
				"depends_on":                 []string{"contacts"},
				"view_extensions":            []map[string]any{{"extends": "contacts.contacts_form", "extension": "hr_job_title"}},
				"view_extension_definitions": []map[string]any{fieldsExtensionDef("hr_job_title", "details")},
			}),
			WasmBytes: okModule,
		},
	}

	modules := LoadAll(context.Background(), rt, testPoolCfg(), sources)

	if hr := modules["hr"]; hr.Status != module.StatusSyncing {
		t.Fatalf("hr.Status = %v, want StatusSyncing; FailureReason = %q", hr.Status, hr.FailureReason)
	}
}
