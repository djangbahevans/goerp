package notify

import (
	"encoding/json/v2"
	"testing"
)

func TestJSONEscapedStrings_EscapesNestedStringsAndKeepsOtherValues(t *testing.T) {
	vars := map[string]any{
		"Name":     `Acme "West"`,
		"Customer": map[string]any{"name": `back\slash`},
		"Lines":    []any{`a "b"`, 3},
		"Tags":     []string{`"x"`},
		"Labels":   map[string]string{"k": `"y"`},
		"Invalid":  "bad\xff\", \"action_url\": \"https://evil",
		"Count":    3,
	}

	got := jsonEscapedStrings(vars)

	doc := `{"a": "` + got["Name"].(string) + `", "b": "` + got["Customer"].(map[string]any)["name"].(string) +
		`", "c": "` + got["Lines"].([]any)[0].(string) + `", "d": "` + got["Tags"].([]string)[0] +
		`", "e": "` + got["Labels"].(map[string]string)["k"] + `", "f": "` + got["Invalid"].(string) + `"}`
	var decoded map[string]string
	if err := json.Unmarshal([]byte(doc), &decoded); err != nil {
		t.Fatalf("escaped values broke the JSON document %s: %v", doc, err)
	}
	want := map[string]string{
		"a": `Acme "West"`, "b": `back\slash`, "c": `a "b"`, "d": `"x"`, "e": `"y"`,
		"f": "bad�\", \"action_url\": \"https://evil",
	}
	if _, injected := decoded["action_url"]; injected {
		t.Errorf("invalid UTF-8 let a key be injected: %v", decoded)
	}
	for k, v := range want {
		if decoded[k] != v {
			t.Errorf("%s round-tripped to %q, want %q", k, decoded[k], v)
		}
	}
	if got["Count"] != 3 || got["Lines"].([]any)[1] != 3 {
		t.Errorf("non-string values changed: %v", got)
	}
	if vars["Name"] != `Acme "West"` || vars["Customer"].(map[string]any)["name"] != `back\slash` {
		t.Errorf("input mutated: %v", vars)
	}
}
