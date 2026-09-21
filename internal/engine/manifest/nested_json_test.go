package manifest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"testing"
)

func TestNestedViewObjectsPreserveUnmodeledMembers(t *testing.T) {
	tests := []struct {
		name  string
		json  string
		into  func() any
		extra func(any) map[string]struct{}
		want  []string
	}{
		{"ListColumn", `{"field":"f","component":"C","component_props":{"a":1}}`,
			func() any { return new(ListColumn) },
			func(v any) map[string]struct{} { return keys(v.(*ListColumn).Extra) },
			[]string{"component", "component_props"}},
		{"Action", `{"label":"L","type":"menu","items":[{"type":"separator"}],"format":"pdf"}`,
			func() any { return new(Action) },
			func(v any) map[string]struct{} { return keys(v.(*Action).Extra) },
			[]string{"format", "items"}},
		{"BulkAction", `{"label":"L","type":"export","format":"csv","min_selected":1,"max_selected":9}`,
			func() any { return new(BulkAction) },
			func(v any) map[string]struct{} { return keys(v.(*BulkAction).Extra) },
			[]string{"format"}},
		{"FormField", `{"field":"f","type":"date_range","range_start_field":"a","range_end_field":"b"}`,
			func() any { return new(FormField) },
			func(v any) map[string]struct{} { return keys(v.(*FormField).Extra) },
			[]string{"range_end_field", "range_start_field"}},
		{"FormSection", `{"type":"custom","component":"C"}`,
			func() any { return new(FormSection) },
			func(v any) map[string]struct{} { return keys(v.(*FormSection).Extra) },
			[]string{"component"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := tt.into()
			if err := json.Unmarshal([]byte(tt.json), v); err != nil {
				t.Fatalf("Unmarshal error: %v", err)
			}
			if got := slices.Sorted(maps.Keys(tt.extra(v))); !slices.Equal(got, tt.want) {
				t.Errorf("Extra keys = %v, want %v", got, tt.want)
			}

			encoded, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("Marshal error: %v", err)
			}
			var got, want map[string]any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatalf("re-decode %s: %v", encoded, err)
			}
			if err := json.Unmarshal([]byte(tt.json), &want); err != nil {
				t.Fatal(err)
			}
			if !mapsEqualJSON(got, want) {
				t.Errorf("round trip = %s, want %s", encoded, tt.json)
			}
		})
	}
}

func keys(m map[string]jsontext.Value) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

func mapsEqualJSON(a, b map[string]any) bool {
	x, _ := json.Marshal(a, json.Deterministic(true))
	y, _ := json.Marshal(b, json.Deterministic(true))
	return string(x) == string(y)
}

func TestBulkActionKeepsSelectionBoundsAndActionMembers(t *testing.T) {
	var b BulkAction
	data := []byte(`{"label":"Archive","type":"route","route":"r","min_selected":2,"max_selected":8,"future":true}`)
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if b.Label != "Archive" || b.Route != "r" || b.MinSelected != 2 || b.MaxSelected != 8 {
		t.Errorf("BulkAction = %+v, want label, route and selection bounds decoded", b)
	}
	if _, ok := b.Extra["min_selected"]; ok {
		t.Error("min_selected is in Extra, want it only in MinSelected")
	}
	if string(b.Extra["future"]) != "true" {
		t.Errorf("Extra[future] = %s, want true", b.Extra["future"])
	}
}

func TestZeroValueFormSectionEncodesAsEmptyObjectWithExtra(t *testing.T) {
	empty, err := json.Marshal(FormSection{})
	if err != nil || string(empty) != "{}" {
		t.Fatalf("Marshal(FormSection{}) = %s, %v, want {}", empty, err)
	}

	withExtra, err := json.Marshal(FormSection{Extra: map[string]jsontext.Value{"component": []byte(`"C"`)}})
	if err != nil || string(withExtra) != `{"component":"C"}` {
		t.Errorf("Marshal(FormSection{Extra}) = %s, %v, want {\"component\":\"C\"}", withExtra, err)
	}
}

func TestNestedObjectsStillValidateModeledMembers(t *testing.T) {
	var c ListColumn
	if err := json.Unmarshal([]byte(`{"field":"f","sortable":"yes"}`), &c); err == nil {
		t.Error("Unmarshal accepted a non-boolean sortable")
	}
}
