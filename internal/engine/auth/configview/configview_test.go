package configview

import (
	"encoding/json/v2"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

func TestEntries(t *testing.T) {
	schema := []manifest.ConfigEntry{
		{Key: "stored", Label: "Stored", Type: "string", Default: "dflt"},
		{Key: "unset", Label: "Unset", Type: "integer", Default: 5},
		{Key: "secret_set", Label: "Secret", Type: "string", Encrypted: true},
		{Key: "secret_unset", Label: "Secret", Type: "string", Encrypted: true, Default: "never shown"},
	}
	rows := map[string]tenantconfig.ModuleConfigRow{
		"stored":     {Value: `"mine"`},
		"secret_set": {Value: `"plaintext"`},
	}

	got := Entries(schema, rows)

	wantValue := map[string]any{"stored": "mine", "unset": float64(5), "secret_set": Masked, "secret_unset": nil}
	wantSet := map[string]bool{"stored": true, "secret_set": true}
	if len(got) != len(schema) {
		t.Fatalf("entries = %d, want %d", len(got), len(schema))
	}
	for i, e := range got {
		if e.Key != schema[i].Key {
			t.Errorf("entry %d key = %q, want schema order %q", i, e.Key, schema[i].Key)
		}
		raw, err := json.Marshal(e.Value)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		if value != wantValue[e.Key] {
			t.Errorf("%s value = %v, want %v", e.Key, value, wantValue[e.Key])
		}
		if e.IsSet != wantSet[e.Key] {
			t.Errorf("%s is_set = %v, want %v", e.Key, e.IsSet, wantSet[e.Key])
		}
	}
}

func TestEntries_EmptySchemaIsAnEmptyArray(t *testing.T) {
	raw, err := json.Marshal(Entries(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Errorf("Entries(nil) = %s, want []", raw)
	}
}
