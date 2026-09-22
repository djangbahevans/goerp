package manifest

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

// validColumnsExtensionDef is a well-formed ViewExtensionDef of type
// "columns" — the simplest type whose payload is a single required slice
// field, reused as a base by the negative-case tests below.
func validColumnsExtensionDef() map[string]any {
	return map[string]any{
		"name":           "hr_employee_column",
		"type":           "columns",
		"target_section": "columns",
		"position":       "append",
		"columns":        []map[string]any{{"field": "hr:employee_id"}},
	}
}

func manifestWithViewExtensions(t *testing.T, dependsOn []string, refs, defs []map[string]any) []byte {
	t.Helper()
	fields := minimalManifestFields()
	fields["depends_on"] = dependsOn
	if refs != nil {
		fields["view_extensions"] = refs
	}
	if defs != nil {
		fields["view_extension_definitions"] = defs
	}
	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return m
}

func TestLoadManifest_ValidViewExtension_Passes(t *testing.T) {
	m := manifestWithViewExtensions(t,
		[]string{"contacts"},
		[]map[string]any{{"extends": "contacts.contacts_list", "extension": "hr_employee_column"}},
		[]map[string]any{validColumnsExtensionDef()},
	)

	if _, err := Load(m); err != nil {
		t.Fatalf("expected manifest with a well-formed view extension to load, got %v", err)
	}
}

func TestLoadManifest_ViewExtensionNoDefinitions_Passes(t *testing.T) {
	m := manifestWithViewExtensions(t, []string{}, nil, nil)
	if _, err := Load(m); err != nil {
		t.Fatalf("expected manifest with no view extensions to load, got %v", err)
	}
}

func TestLoadManifest_ExtendsMalformed_Rejected(t *testing.T) {
	m := manifestWithViewExtensions(t,
		[]string{"contacts"},
		[]map[string]any{{"extends": "contacts", "extension": "hr_employee_column"}}, // missing .{view_name}
		[]map[string]any{validColumnsExtensionDef()},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with a malformed extends value to be rejected")
	}
	if !strings.Contains(err.Error(), "must be {module}.{view_name}") {
		t.Fatalf("expected error to describe the required extends format, got: %v", err)
	}
}

func TestLoadManifest_ExtendsModuleNotDeclaredDependency_Rejected(t *testing.T) {
	m := manifestWithViewExtensions(t,
		[]string{}, // contacts not declared
		[]map[string]any{{"extends": "contacts.contacts_list", "extension": "hr_employee_column"}},
		[]map[string]any{validColumnsExtensionDef()},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest extending an undeclared module's view to be rejected")
	}
	if !strings.Contains(err.Error(), "not in depends_on or soft_depends_on") {
		t.Fatalf("expected error to mention the missing dependency declaration, got: %v", err)
	}
}

func TestLoadManifest_ExtendsModuleInSoftDependsOn_Passes(t *testing.T) {
	fields := minimalManifestFields()
	fields["depends_on"] = []string{}
	fields["soft_depends_on"] = []string{"contacts"}
	fields["view_extensions"] = []map[string]any{{"extends": "contacts.contacts_list", "extension": "hr_employee_column"}}
	fields["view_extension_definitions"] = []map[string]any{validColumnsExtensionDef()}
	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	if _, err := Load(m); err != nil {
		t.Fatalf("expected manifest extending a soft dependency's view to load, got %v", err)
	}
}

func TestLoadManifest_ExtensionNameUnresolved_Rejected(t *testing.T) {
	m := manifestWithViewExtensions(t,
		[]string{"contacts"},
		[]map[string]any{{"extends": "contacts.contacts_list", "extension": "does_not_exist"}},
		[]map[string]any{validColumnsExtensionDef()},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest referencing an undefined extension name to be rejected")
	}
	if !strings.Contains(err.Error(), "does not name a definition") {
		t.Fatalf("expected error to mention the unresolved extension name, got: %v", err)
	}
}

func TestLoadManifest_DuplicateDefinitionNames_Rejected(t *testing.T) {
	dupA := validColumnsExtensionDef()
	dupB := validColumnsExtensionDef()
	m := manifestWithViewExtensions(t,
		[]string{"contacts"},
		[]map[string]any{{"extends": "contacts.contacts_list", "extension": "hr_employee_column"}},
		[]map[string]any{dupA, dupB},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with duplicate view_extension_definitions names to be rejected")
	}
	if !strings.Contains(err.Error(), "must be unique within the manifest") {
		t.Fatalf("expected error to mention the duplicate name, got: %v", err)
	}
}

func TestLoadManifest_ExtensionDefInvalidType_Rejected(t *testing.T) {
	def := validColumnsExtensionDef()
	def["type"] = "replace_everything"
	m := manifestWithViewExtensions(t,
		[]string{"contacts"},
		[]map[string]any{{"extends": "contacts.contacts_list", "extension": "hr_employee_column"}},
		[]map[string]any{def},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with an invalid extension type to be rejected")
	}
	if !strings.Contains(err.Error(), "must be one of") {
		t.Fatalf("expected error to list the valid extension types, got: %v", err)
	}
}

func TestLoadManifest_ExtensionDefInvalidPosition_Rejected(t *testing.T) {
	def := validColumnsExtensionDef()
	def["position"] = "replace"
	m := manifestWithViewExtensions(t,
		[]string{"contacts"},
		[]map[string]any{{"extends": "contacts.contacts_list", "extension": "hr_employee_column"}},
		[]map[string]any{def},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf(`expected manifest with position "replace" to be rejected`)
	}
	if !strings.Contains(err.Error(), `must be "append" or "prepend"`) {
		t.Fatalf("expected error to describe the allowed positions, got: %v", err)
	}
}

func TestLoadManifest_ExtensionDefEmptyTargetSection_Rejected(t *testing.T) {
	def := validColumnsExtensionDef()
	def["target_section"] = ""
	m := manifestWithViewExtensions(t,
		[]string{"contacts"},
		[]map[string]any{{"extends": "contacts.contacts_list", "extension": "hr_employee_column"}},
		[]map[string]any{def},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with an empty target_section to be rejected")
	}
	if !strings.Contains(err.Error(), "target_section must be non-empty") {
		t.Fatalf("expected error to mention target_section, got: %v", err)
	}
}

func TestLoadManifest_ExtensionDefMissingPayload_Rejected(t *testing.T) {
	def := map[string]any{
		"name":           "hr_employment_tab",
		"type":           "tab",
		"target_section": "tabs",
		"position":       "append",
		// no "tab" payload
	}
	m := manifestWithViewExtensions(t,
		[]string{"contacts"},
		[]map[string]any{{"extends": "contacts.contacts_form", "extension": "hr_employment_tab"}},
		[]map[string]any{def},
	)

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected manifest with a tab-type definition missing its tab payload to be rejected")
	}
	if !strings.Contains(err.Error(), `requires a tab payload`) {
		t.Fatalf("expected error to mention the missing tab payload, got: %v", err)
	}
}
