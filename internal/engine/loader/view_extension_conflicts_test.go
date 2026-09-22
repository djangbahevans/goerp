package loader

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// captureLog temporarily redirects the global zerolog logger to a buffer
// for the duration of a test, restoring it on cleanup (mirrors
// notiftemplate_test.go's own local helper — no shared testing package
// precedent for this yet).
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	original := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = original })
	return &buf
}

func TestLogViewExtensionConflicts_TwoModulesSameLocation_LogsOneWarningNamingBoth(t *testing.T) {
	buf := captureLog(t)

	LogViewExtensionConflicts([]AppliedViewExtension{
		{Module: "hr", LoadOrder: 1, TargetView: "contacts.contacts_form", TargetSection: "tabs", Position: "prepend", Type: "tab", TabLabel: "Employment"},
		{Module: "payroll", LoadOrder: 2, TargetView: "contacts.contacts_form", TargetSection: "tabs", Position: "prepend", Type: "tab", TabLabel: "Employment"},
	})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one warning line, got %d: %s", len(lines), buf.String())
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}

	want := map[string]string{
		"view":       "contacts.contacts_form",
		"section":    "tabs",
		"position":   "prepend",
		"module_a":   `hr (tab: "Employment")`,
		"module_b":   `payroll (tab: "Employment")`,
		"resolution": "hr applied first (hr has lower dependency order)",
		"message":    "view extension conflict",
	}
	for field, wantVal := range want {
		if got, _ := entry[field].(string); got != wantVal {
			t.Errorf("field %q = %q, want %q", field, got, wantVal)
		}
	}
}

func TestLogViewExtensionConflicts_NonTabType_ModuleLabelHasNoTabSuffix(t *testing.T) {
	buf := captureLog(t)

	LogViewExtensionConflicts([]AppliedViewExtension{
		{Module: "hr", LoadOrder: 1, TargetView: "contacts.contacts_list", TargetSection: "columns", Position: "append", Type: "columns"},
		{Module: "payroll", LoadOrder: 2, TargetView: "contacts.contacts_list", TargetSection: "columns", Position: "append", Type: "columns"},
	})

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if got := entry["module_a"]; got != "hr" {
		t.Errorf(`module_a = %v, want "hr" (no tab suffix for a "columns" extension)`, got)
	}
}

func TestLogViewExtensionConflicts_SameModuleTwiceAtOneLocation_NoWarning(t *testing.T) {
	buf := captureLog(t)

	LogViewExtensionConflicts([]AppliedViewExtension{
		{Module: "hr", LoadOrder: 1, TargetView: "contacts.contacts_form", TargetSection: "tabs", Position: "prepend", Type: "tab", TabLabel: "Employment"},
		{Module: "hr", LoadOrder: 1, TargetView: "contacts.contacts_form", TargetSection: "tabs", Position: "prepend", Type: "tab", TabLabel: "Salary"},
	})

	if buf.Len() != 0 {
		t.Fatalf("expected no warning for a single module contributing twice, got: %s", buf.String())
	}
}

func TestLogViewExtensionConflicts_DifferentSectionsOrPositions_NoWarning(t *testing.T) {
	buf := captureLog(t)

	LogViewExtensionConflicts([]AppliedViewExtension{
		{Module: "hr", LoadOrder: 1, TargetView: "contacts.contacts_form", TargetSection: "tabs", Position: "append", Type: "tab", TabLabel: "Employment"},
		{Module: "payroll", LoadOrder: 2, TargetView: "contacts.contacts_form", TargetSection: "tabs", Position: "prepend", Type: "tab", TabLabel: "Employment"},
	})

	if buf.Len() != 0 {
		t.Fatalf("expected no warning for extensions at different positions, got: %s", buf.String())
	}
}

func TestLogViewExtensionConflicts_ThreeModulesSameLocation_LogsOnePerAdjacentPair(t *testing.T) {
	buf := captureLog(t)

	LogViewExtensionConflicts([]AppliedViewExtension{
		{Module: "hr", LoadOrder: 1, TargetView: "contacts.contacts_form", TargetSection: "tabs", Position: "prepend", Type: "tab", TabLabel: "Employment"},
		{Module: "payroll", LoadOrder: 2, TargetView: "contacts.contacts_form", TargetSection: "tabs", Position: "prepend", Type: "tab", TabLabel: "Employment"},
		{Module: "benefits", LoadOrder: 3, TargetView: "contacts.contacts_form", TargetSection: "tabs", Position: "prepend", Type: "tab", TabLabel: "Employment"},
	})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 warnings for a 3-module conflict group (N-1 adjacent pairs), got %d: %s", len(lines), buf.String())
	}
}

func TestLoadAll_ViewExtensionConflict_LogsWarningAndBothLoad(t *testing.T) {
	buf := captureLog(t)
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
				"depends_on":      []string{"contacts"},
				"view_extensions": []map[string]any{{"extends": "contacts.contacts_form", "extension": "hr_employment_tab"}},
				"view_extension_definitions": []map[string]any{{
					"name": "hr_employment_tab", "type": "tab", "target_section": "tabs", "position": "prepend",
					"tab": map[string]any{"label": "Employment", "type": "component", "component": "EmploymentTab"},
				}},
			}),
			WasmBytes: okModule,
		},
		{
			Name: "payroll",
			ManifestBytes: manifestJSONWithFields(t, "payroll", okModule, []string{"db.read"}, map[string]any{
				"depends_on":      []string{"contacts"},
				"view_extensions": []map[string]any{{"extends": "contacts.contacts_form", "extension": "payroll_employment_tab"}},
				"view_extension_definitions": []map[string]any{{
					"name": "payroll_employment_tab", "type": "tab", "target_section": "tabs", "position": "prepend",
					"tab": map[string]any{"label": "Employment", "type": "component", "component": "PayrollTab"},
				}},
			}),
			WasmBytes: okModule,
		},
	}

	modules := LoadAll(context.Background(), rt, testPoolCfg(), sources)

	if hr := modules["hr"]; hr.Status != module.StatusSyncing {
		t.Fatalf("hr.Status = %v, want StatusSyncing; FailureReason = %q", hr.Status, hr.FailureReason)
	}
	if payroll := modules["payroll"]; payroll.Status != module.StatusSyncing {
		t.Fatalf("payroll.Status = %v, want StatusSyncing; FailureReason = %q", payroll.Status, payroll.FailureReason)
	}

	if !strings.Contains(buf.String(), "view extension conflict") {
		t.Errorf("expected a logged view extension conflict, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `tab: \"Employment\"`) {
		t.Errorf("expected the conflict log to name the colliding tab label, got: %s", buf.String())
	}
}
