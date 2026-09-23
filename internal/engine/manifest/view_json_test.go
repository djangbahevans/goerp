package manifest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

const viewFixtureDir = "../../../testdata/manifest-views"

func readViewFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(viewFixtureDir, "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no view fixtures under %s (err: %v)", viewFixtureDir, err)
	}
	fixtures := make(map[string][]byte, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		fixtures[filepath.Base(p)] = data
	}
	return fixtures
}

func decodeAny(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return m
}

func TestViewPreservesUnmodeledMembers(t *testing.T) {
	for name, data := range readViewFixtures(t) {
		t.Run(name, func(t *testing.T) {
			declared := decodeAny(t, data)

			var view View
			if err := json.Unmarshal(data, &view); err != nil {
				t.Fatalf("Unmarshal error: %v", err)
			}

			wantExtra := map[string]any{}
			for member, value := range declared {
				if _, modeled := typedMemberNames(reflect.TypeFor[viewAlias]())[member]; !modeled || viewForceExtra(declared["type"].(string))(member) {
					wantExtra[member] = value
				}
			}
			gotExtra := map[string]any{}
			for member, raw := range view.Extra {
				gotExtra[member] = decodeAny(t, []byte(`{"v":`+string(raw)+`}`))["v"]
			}
			if !reflect.DeepEqual(gotExtra, wantExtra) {
				t.Errorf("Extra = %v, want %v", gotExtra, wantExtra)
			}

			encoded, err := json.Marshal(view)
			if err != nil {
				t.Fatalf("Marshal error: %v", err)
			}
			served := decodeAny(t, encoded)
			for member, want := range wantExtra {
				if !reflect.DeepEqual(served[member], want) {
					t.Errorf("served %q = %v, want %v", member, served[member], want)
				}
			}
			for _, member := range []string{"name", "type", "resource", "label"} {
				if served[member] != declared[member] {
					t.Errorf("served %q = %v, want %v", member, served[member], declared[member])
				}
			}
		})
	}
}

func TestViewMarshalEmitsModeledMembersOnce(t *testing.T) {
	view := View{
		Name: "v", Type: "list", Resource: "m.r", Label: "V",
		Extra: map[string]jsontext.Value{"name": jsontext.Value(`"shadow"`), "custom_member": jsontext.Value(`1`)},
	}

	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	served := decodeAny(t, encoded)
	if served["name"] != "v" {
		t.Errorf("name = %v, want the typed value %q", served["name"], "v")
	}
	if served["custom_member"] != 1.0 {
		t.Errorf("custom_member = %v, want 1", served["custom_member"])
	}
}

func TestViewMarshalIsDeterministic(t *testing.T) {
	data := readViewFixtures(t)["kanban.json"]
	var view View
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	first, _ := json.Marshal(view)
	for range 20 {
		again, _ := json.Marshal(view)
		if string(again) != string(first) {
			t.Fatalf("Marshal output varies:\n%s\n%s", first, again)
		}
	}
}

func TestViewPivotColumnsBypassTypedField(t *testing.T) {
	var view View
	data := []byte(`{"name":"p","type":"pivot","resource":"m.r","label":"P","columns":["state"]}`)
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if view.Columns != nil {
		t.Errorf("Columns = %v, want nil for a pivot view", view.Columns)
	}
	if got := slices.Sorted(maps.Keys(view.Extra)); !slices.Equal(got, []string{"columns"}) {
		t.Errorf("Extra keys = %v, want [columns]", got)
	}
}

func TestViewStillValidatesModeledMembers(t *testing.T) {
	var view View
	data := []byte(`{"name":"l","type":"list","resource":"m.r","label":"L","columns":"not-an-array"}`)
	if err := json.Unmarshal(data, &view); err == nil {
		t.Error("Unmarshal accepted a list view whose columns is not an array")
	}
}

func TestViewSearchParamRoundTrips(t *testing.T) {
	view := View{Name: "l", Type: "list", Resource: "m.r", Label: "L", SearchParam: "search"}

	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	served := decodeAny(t, encoded)
	if served["search_param"] != "search" {
		t.Errorf("served search_param = %v, want %q", served["search_param"], "search")
	}

	var decoded View
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.SearchParam != "search" {
		t.Errorf("decoded SearchParam = %q, want %q", decoded.SearchParam, "search")
	}
}

func TestViewKeepsTypeSpecificMembersInExtra(t *testing.T) {
	want := map[string][]string{
		"kanban.json":    {"group_by", "card_fields", "group_values", "quick_create", "allow_drag", "max_cards_per_column"},
		"calendar.json":  {"date_field", "title_field", "color_map", "default_view", "allowed_views", "on_click", "quick_create"},
		"timeline.json":  {"start_field", "end_field", "group_by", "color_field", "allow_resize", "default_range"},
		"pivot.json":     {"rows", "columns", "values", "allow_download", "use_wasm"},
		"list-tree.json": {"tree_field", "default_expanded_depth"},
	}
	fixtures := readViewFixtures(t)
	for name, members := range want {
		t.Run(name, func(t *testing.T) {
			var view View
			if err := json.Unmarshal(fixtures[name], &view); err != nil {
				t.Fatalf("Unmarshal error: %v", err)
			}
			for _, member := range members {
				if _, ok := view.Extra[member]; !ok {
					t.Errorf("Extra lacks %q; has %v", member, slices.Sorted(maps.Keys(view.Extra)))
				}
			}
		})
	}
}
