package manifest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestLoadConfigSeedTypes(t *testing.T) {
	tests := []struct {
		name  string
		typ   string
		value any
		valid bool
	}{
		{name: "string", typ: "string", value: "GH", valid: true},
		{name: "integer", typ: "integer", value: 42, valid: true},
		{name: "negative integer", typ: "integer", value: -42, valid: true},
		{name: "whole float as integer", typ: "integer", value: 42.0, valid: true},
		{name: "integer exponent", typ: "integer", value: jsontext.Value(`1e3`), valid: true},
		{name: "integer decimal", typ: "integer", value: jsontext.Value(`42.0`), valid: true},
		{name: "maximum integer", typ: "integer", value: jsontext.Value(`9223372036854775807`), valid: true},
		{name: "minimum integer", typ: "integer", value: jsontext.Value(`-9223372036854775808`), valid: true},
		{name: "float", typ: "float", value: 0.85, valid: true},
		{name: "boolean", typ: "boolean", value: false, valid: true},
		{name: "duration", typ: "duration", value: "1h30m", valid: true},
		{name: "json object", typ: "json", value: map[string]any{"enabled": true}, valid: true},
		{name: "json null", typ: "json", valid: true},
		{name: "strings", typ: "string[]", value: []string{"GH", "KE"}, valid: true},
		{name: "integers", typ: "integer[]", value: []int{1, -2}, valid: true},
		{name: "floats", typ: "float[]", value: []float64{1, 2.5}, valid: true},
		{name: "empty strings", typ: "string[]", value: []string{}, valid: true},
		{name: "empty integers", typ: "integer[]", value: []int{}, valid: true},
		{name: "empty floats", typ: "float[]", value: []float64{}, valid: true},
		{name: "number as string", typ: "string", value: 1},
		{name: "fractional integer", typ: "integer", value: 1.5},
		{name: "integer overflow", typ: "integer", value: 1e20},
		{name: "fraction rounded by float64", typ: "integer", value: jsontext.Value(`9007199254740992.5`)},
		{name: "tiny fraction rounded by float64", typ: "integer", value: jsontext.Value(`1.0000000000000000001`)},
		{name: "fractional array rounded by float64", typ: "integer[]", value: jsontext.Value(`[9007199254740992.5]`)},
		{name: "string as number", typ: "float", value: "0.5"},
		{name: "string as boolean", typ: "boolean", value: "true"},
		{name: "invalid duration", typ: "duration", value: "soon"},
		{name: "numeric duration", typ: "duration", value: 60},
		{name: "mixed strings", typ: "string[]", value: []any{"GH", 1}},
		{name: "fractional array element", typ: "integer[]", value: []float64{1, 2.5}},
		{name: "string in numeric array", typ: "float[]", value: []any{1, "2"}},
		{name: "scalar as array", typ: "string[]", value: "GH"},
		{name: "null string", typ: "string"},
		{name: "null integer", typ: "integer"},
		{name: "null float", typ: "float"},
		{name: "null boolean", typ: "boolean"},
		{name: "null duration", typ: "duration"},
		{name: "null array", typ: "string[]"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fields := minimalManifestFields()
			fields["config_schema"] = []ConfigEntry{{Key: "setting", Type: test.typ}}
			fields["tenant_config_seeds"] = map[string]any{"demo.setting": test.value}
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}

			_, err = Load(raw)

			if test.valid {
				if err != nil {
					t.Fatalf("valid seed rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), `tenant_config_seeds "demo.setting"`) {
				t.Fatalf("invalid seed: error = %v, want key-specific rejection", err)
			}
		})
	}
}

func TestLoadConfigSeedKeys(t *testing.T) {
	for _, key := range []string{"setting", "other.setting", "demo.missing", "demo.demo.setting", "demo."} {
		t.Run(key, func(t *testing.T) {
			fields := minimalManifestFields()
			fields["config_schema"] = []ConfigEntry{{Key: "setting", Type: "boolean"}}
			fields["tenant_config_seeds"] = map[string]any{key: true}
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}

			_, err = Load(raw)

			if err == nil || !strings.Contains(err.Error(), "tenant_config_seeds") || !strings.Contains(err.Error(), key) {
				t.Fatalf("invalid key: error = %v, want key-specific rejection", err)
			}
		})
	}
}
