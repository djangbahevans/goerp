package manifest

import (
	"encoding/json/v2"
	"maps"
	"strings"
	"testing"
)

func manifestWithConfig(t *testing.T, dependsOn, softDependsOn []string, schema, uses []map[string]any) []byte {
	t.Helper()
	fields := minimalManifestFields()
	fields["depends_on"] = dependsOn
	if softDependsOn != nil {
		fields["soft_depends_on"] = softDependsOn
	}
	if schema != nil {
		fields["config_schema"] = schema
	}
	if uses != nil {
		fields["uses_config"] = uses
	}
	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return m
}

func TestLoadManifest_ConfigSchema(t *testing.T) {
	durationEntry := func(extra map[string]any) []map[string]any {
		entry := map[string]any{"key": "reconcile_interval", "label": "Interval", "type": "duration", "default": "15m"}
		maps.Copy(entry, extra)
		return []map[string]any{entry}
	}

	tests := []struct {
		name    string
		schema  []map[string]any
		wantErr string
	}{
		{"duration with duration-string bounds", durationEntry(map[string]any{"min": "1m", "max": "1h"}), ""},
		{"integer with numeric bounds", []map[string]any{{"key": "n", "label": "N", "type": "integer", "default": 1, "min": 0, "max": 10}}, ""},
		{"unknown type", []map[string]any{{"key": "k", "label": "K", "type": "decimal"}}, `type "decimal" must be one of`},
		{"duplicate key", append(durationEntry(nil), durationEntry(nil)...), `key "reconcile_interval" declared more than once`},
		{"duration default that does not parse", durationEntry(map[string]any{"default": "soon"}), "must be a Go duration string"},
		{"duration bound given as a number", durationEntry(map[string]any{"min": 60}), "min 60 must be a Go duration string"},
		{"duration bound that does not parse", durationEntry(map[string]any{"max": "forever"}), "max forever must be a Go duration string"},
		{"numeric bound given as a string", []map[string]any{{"key": "n", "label": "N", "type": "float", "min": "1m"}}, "must be a number"},
		{"bound on a string key", []map[string]any{{"key": "s", "label": "S", "type": "string", "max": 5}}, `is not valid for type "string"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(manifestWithConfig(t, []string{}, nil, tt.schema, nil))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("expected the manifest to load, got %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadManifest_UsesConfig(t *testing.T) {
	ref := func(key, typ string) []map[string]any {
		return []map[string]any{{"key": key, "type": typ}}
	}

	tests := []struct {
		name    string
		depends []string
		soft    []string
		uses    []map[string]any
		wantErr string
	}{
		{"hard dependency", []string{"l10n_gh"}, nil, ref("l10n_gh.vat_cert_number", "string"), ""},
		{"soft dependency", []string{}, []string{"l10n_gh"}, ref("l10n_gh.vat_cert_number", "string"), ""},
		{"not a dependency", []string{}, nil, ref("l10n_gh.vat_cert_number", "string"), "not in depends_on or soft_depends_on"},
		{"no module segment", []string{"l10n_gh"}, nil, ref("vat_cert_number", "string"), "must be {module}.{key}"},
		{"own key", []string{}, nil, ref("demo.key", "string"), "is this module's own key"},
		{"unknown type", []string{"l10n_gh"}, nil, ref("l10n_gh.k", "decimal"), `type "decimal" must be one of`},
		{"duplicate", []string{"l10n_gh"}, nil, append(ref("l10n_gh.k", "string"), ref("l10n_gh.k", "string")...), "listed more than once"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(manifestWithConfig(t, tt.depends, tt.soft, nil, tt.uses))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("expected the manifest to load, got %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadManifest_ConfigFieldTypes(t *testing.T) {
	tests := []struct {
		name    string
		typ     string
		field   string
		options []map[string]string
		want    string
	}{
		{
			name:  "country",
			typ:   "string",
			field: "country_select",
		},
		{
			name:    "scalar select",
			typ:     "string",
			field:   "select",
			options: []map[string]string{{"value": "GH", "label": "Ghana"}},
		},
		{
			name:    "numeric radio",
			typ:     "integer",
			field:   "radio",
			options: []map[string]string{{"value": "1", "label": "One"}},
		},
		{
			name:    "array select",
			typ:     "string[]",
			field:   "select",
			options: []map[string]string{{"value": "GH", "label": "Ghana"}},
			want:    "incompatible",
		},
		{
			name:    "scalar multiselect",
			typ:     "string",
			field:   "multiselect",
			options: []map[string]string{{"value": "GH", "label": "Ghana"}},
			want:    "incompatible",
		},
		{
			name:    "numeric multiselect",
			typ:     "float[]",
			field:   "multi_select",
			options: []map[string]string{{"value": "0.5", "label": "Half"}},
		},
		{
			name:  "missing options",
			typ:   "string",
			field: "select",
			want:  "requires non-empty options",
		},
		{
			name:  "model metadata",
			typ:   "string",
			field: "relation",
			want:  "unsupported settings field_type",
		},
		{
			name:  "wrong country storage",
			typ:   "integer",
			field: "country_select",
			want:  "incompatible",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := map[string]any{
				"key":        "setting",
				"label":      "Setting",
				"type":       test.typ,
				"field_type": test.field,
				"options":    test.options,
			}
			_, err := Load(manifestWithConfig(t, []string{}, nil, []map[string]any{entry}, nil))

			if test.want == "" {
				if err != nil {
					t.Fatalf("load: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("load = %v, want %q", err, test.want)
			}
		})
	}
}
