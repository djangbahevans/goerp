package module

import (
	"slices"
	"testing"

	"encoding/json/jsontext"
)

func block(key, value string) generatedBlock {
	b := generatedBlock{key: key}
	if value != "" {
		b.value = jsontext.Value(value)
	}
	return b
}

func TestRewriteManifest(t *testing.T) {
	const indented = "{\n  \"name\": \"demo\",\n  \"emits\": [\n    {\"name\": \"old\"}\n  ],\n  \"version\":   \"1.0.0\"\n}\n"

	tests := []struct {
		name        string
		raw         string
		blocks      []generatedBlock
		want        string
		wantChanged []string
	}{
		{
			name:   "current value formatted differently is left alone",
			raw:    indented,
			blocks: []generatedBlock{block("emits", `[{"name":"old"}]`)},
			want:   indented,
		},
		{
			name:        "changed value replaces only that value",
			raw:         indented,
			blocks:      []generatedBlock{block("emits", `[{"name":"new"}]`)},
			want:        "{\n  \"name\": \"demo\",\n  \"emits\": [\n    {\n      \"name\": \"new\"\n    }\n  ],\n  \"version\":   \"1.0.0\"\n}\n",
			wantChanged: []string{"emits"},
		},
		{
			name:        "new key is appended in the manifest's indentation",
			raw:         indented,
			blocks:      []generatedBlock{block("subscribes", `["a"]`)},
			want:        "{\n  \"name\": \"demo\",\n  \"emits\": [\n    {\"name\": \"old\"}\n  ],\n  \"version\":   \"1.0.0\",\n  \"subscribes\": [\n    \"a\"\n  ]\n}\n",
			wantChanged: []string{"subscribes"},
		},
		{
			name:        "undeclared key is removed from the middle",
			raw:         indented,
			blocks:      []generatedBlock{block("emits", "")},
			want:        "{\n  \"name\": \"demo\",\n  \"version\":   \"1.0.0\"\n}\n",
			wantChanged: []string{"emits"},
		},
		{
			name:        "undeclared first and last keys are removed",
			raw:         "{\n\t\"a\": 1,\n\t\"b\": 2,\n\t\"c\": 3\n}",
			blocks:      []generatedBlock{block("a", ""), block("c", "")},
			want:        "{\n\t\"b\": 2\n}",
			wantChanged: []string{"a", "c"},
		},
		{
			name:        "compact manifest stays compact",
			raw:         `{"name":"demo","emits":[]}`,
			blocks:      []generatedBlock{block("emits", `[{"name": "x"}]`), block("jobs", `{"a":1}`)},
			want:        `{"name":"demo","emits":[{"name":"x"}],"jobs":{"a":1}}`,
			wantChanged: []string{"emits", "jobs"},
		},
		{
			name:   "absent and undeclared is a no-op",
			raw:    indented,
			blocks: []generatedBlock{block("subscribes", "")},
			want:   indented,
		},
		{
			name:        "tab indentation is reused",
			raw:         "{\n\t\"name\": \"demo\"\n}\n",
			blocks:      []generatedBlock{block("emits", `[1]`)},
			want:        "{\n\t\"name\": \"demo\",\n\t\"emits\": [\n\t\t1\n\t]\n}\n",
			wantChanged: []string{"emits"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := rewriteManifest([]byte(tt.raw), tt.blocks)
			if err != nil {
				t.Fatalf("rewriteManifest: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("manifest =\n%q\nwant\n%q", got, tt.want)
			}
			if !slices.Equal(changed, tt.wantChanged) {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
		})
	}
}

func TestRewriteManifest_UnchangedReturnsTheInputBytes(t *testing.T) {
	raw := []byte("{\"name\":\"demo\",\n\n   \"emits\" : []  }")

	got, changed, err := rewriteManifest(raw, []generatedBlock{block("emits", "[]")})
	if err != nil || len(changed) != 0 || &got[0] != &raw[0] {
		t.Fatalf("got %q, %v, %v; want the input slice and no changes", got, changed, err)
	}
}

func TestRewriteManifest_Errors(t *testing.T) {
	if _, _, err := rewriteManifest([]byte(`[1]`), []generatedBlock{block("a", "1")}); err == nil {
		t.Error("a manifest that is not an object was accepted")
	}
	if _, _, err := rewriteManifest([]byte(`{"a": 1`), []generatedBlock{block("a", "2")}); err == nil {
		t.Error("a truncated manifest was accepted")
	}
	if _, _, err := rewriteManifest([]byte(`{"a": 1}`), []generatedBlock{block("a", "")}); err == nil {
		t.Error("removing every key was accepted")
	}
}
