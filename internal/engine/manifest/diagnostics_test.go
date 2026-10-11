package manifest

import (
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestLoadWarningDiagnostics(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		rules []string
	}{
		{
			name: "missing required and optional seeds",
			extra: map[string]any{
				"config_schema": []ConfigEntry{
					{Key: "required", Type: "string", Required: true},
					{Key: "optional", Type: "boolean"},
				},
			},
			rules: []string{RuleMissingSeed, RuleMissingSeed},
		},
		{
			name: "qualified seeds including null",
			extra: map[string]any{
				"config_schema": []ConfigEntry{
					{Key: "enabled", Type: "boolean"},
					{Key: "label", Type: "json"},
				},
				"tenant_config_seeds": map[string]any{"demo.enabled": false, "demo.label": nil},
			},
		},
		{
			name: "unknown role",
			extra: map[string]any{
				"permissions": []Permission{{Name: "demo:order:read", DefaultRoles: []string{"admin", "custom"}}},
			},
			rules: []string{RuleUnknownRole},
		},
		{
			name: "HTTP allowed with warning",
			extra: map[string]any{
				"type":           "connector",
				"capabilities":   []string{"http.fetch"},
				"http_allowlist": []string{"http://example.test/*", "https://secure.test/*"},
			},
			rules: []string{RuleInsecureHTTP},
		},
		{
			name:  "long description",
			extra: map[string]any{"long_description": strings.Repeat("界", 4097)},
			rules: []string{RuleLongDescription},
		},
		{
			name:  "description counts characters",
			extra: map[string]any{"long_description": strings.Repeat("界", 4096)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fields := minimalManifestFields()
			fields["schema"] = map[string]any{"owned_models": []string{}}
			maps.Copy(fields, tc.extra)

			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}

			mf, err := Load(raw)
			if err != nil {
				t.Fatalf("warning-only manifest failed: %v", err)
			}

			var rules []string
			for _, d := range mf.Diagnostics {
				rules = append(rules, d.Rule)
				if d.Severity != "warning" || d.Module != "demo" || d.Message == "" || len(d.Context) == 0 {
					t.Fatalf("incomplete diagnostic: %+v", d)
				}
			}

			if !slices.Equal(rules, tc.rules) {
				t.Fatalf("rules = %v, want %v", rules, tc.rules)
			}
		})
	}
}

func TestLoadSupportedDeclarationErrors(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{
			name:  "invalid name",
			extra: map[string]any{"name": "Invalid-name"},
			want:  "Name",
		},
		{
			name:  "invalid version",
			extra: map[string]any{"version": "broken"},
			want:  "Version",
		},
		{
			name:  "unsupported ABI",
			extra: map[string]any{"abi_version": "2"},
			want:  "unsupported",
		},
		{
			name:  "allowlist without capability",
			extra: map[string]any{"http_allowlist": []string{"http://example.test/*"}},
			want:  "http.fetch",
		},
		{
			name: "duplicate workflow",
			extra: map[string]any{
				"worker_checksum": "sha256:test",
				"workflow_types":  []WorkflowType{{Name: "sync"}, {Name: "sync"}},
			},
			want: "duplicate name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fields := minimalManifestFields()
			maps.Copy(fields, tc.extra)

			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := Load(raw); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want %q", err, tc.want)
			}
		})
	}
}
