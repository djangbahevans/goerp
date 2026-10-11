package module

import (
	"encoding/json/jsontext"
	"slices"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestOwnedModelsCollector(t *testing.T) {
	d := Declarations{model.KindModel: {
		jsontext.Value(`{"name":"widgets.zeta"}`),
		jsontext.Value(`{"name":"alpha"}`),
	}}

	if got := collectJSON(t, ownedModelsCollector{}, d, ModuleInfo{Name: "widgets"}); got != `["widgets.alpha","widgets.zeta"]` {
		t.Errorf("owned_models = %s", got)
	}

	blocks, err := collectBlocks(nil, ModuleInfo{Name: "widgets"}, []Collector{ownedModelsCollector{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].key != "schema.owned_models" || string(blocks[0].value) != "[]" {
		t.Errorf("empty models block = %v, want schema.owned_models: []", blocks)
	}
}

func TestOwnedModelsCollectorValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  []jsontext.Value
		want string
	}{
		{
			name: "duplicate",
			raw:  []jsontext.Value{jsontext.Value(`{"name":"widgets.item"}`), jsontext.Value(`{"name":"widgets.item"}`)},
			want: "declared more than once",
		},
		{
			name: "foreign owner",
			raw:  []jsontext.Value{jsontext.Value(`{"name":"sales.item"}`)},
			want: "module's own",
		},
		{
			name: "duplicate after qualification",
			raw:  []jsontext.Value{jsontext.Value(`{"name":"widgets.item"}`), jsontext.Value(`{"name":"item"}`)},
			want: "declared more than once",
		},
		{
			name: "empty name",
			raw:  []jsontext.Value{jsontext.Value(`{"name":""}`)},
			want: "module's own",
		},
		{
			name: "empty resource",
			raw:  []jsontext.Value{jsontext.Value(`{"name":"widgets."}`)},
			want: "module's own",
		},
		{
			name: "extra segment",
			raw:  []jsontext.Value{jsontext.Value(`{"name":"widgets.item.child"}`)},
			want: "module's own",
		},
		{
			name: "malformed declaration",
			raw:  []jsontext.Value{jsontext.Value(`[]`)},
			want: "decode model",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := (ownedModelsCollector{}).Collect(Declarations{model.KindModel: test.raw}, ModuleInfo{Name: "widgets"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Collect = %v, want %s", err, test.want)
			}
		})
	}
}

func TestRewriteManifestNestedBlocks(t *testing.T) {
	for _, test := range []struct {
		name   string
		raw    string
		blocks []generatedBlock
		want   string
	}{
		{
			name:   "preserve schema siblings exactly",
			raw:    `{"name":"widgets","schema" : { "has_data_migrations" : true , "owned_models" : [ "old.model" ], "extends_models": [ "core.base" ], "extra" : { "n" : 1e3 } },"version": "1.0.0"}`,
			blocks: []generatedBlock{block("schema.owned_models", `["widgets.item"]`)},
			want:   `{"name":"widgets","schema" : { "has_data_migrations" : true , "owned_models" : ["widgets.item"], "extends_models": [ "core.base" ], "extra" : { "n" : 1e3 } },"version": "1.0.0"}`,
		},
		{
			name:   "insert schema",
			raw:    `{"name":"widgets"}`,
			blocks: []generatedBlock{block("schema.owned_models", `[]`)},
			want:   `{"name":"widgets","schema":{"owned_models":[]}}`,
		},
		{
			name:   "insert into empty schema",
			raw:    `{"name":"widgets","schema":{}}`,
			blocks: []generatedBlock{block("schema.owned_models", `[]`)},
			want:   `{"name":"widgets","schema":{"owned_models":[]}}`,
		},
		{
			name:   "clear models and keep siblings",
			raw:    "{\n  \"schema\": {\n    \"owned_models\": [\"widgets.item\"],\n    \"has_data_migrations\" : true\n  }\n}\n",
			blocks: []generatedBlock{block("schema.owned_models", `[]`)},
			want:   "{\n  \"schema\": {\n    \"owned_models\": [],\n    \"has_data_migrations\" : true\n  }\n}\n",
		},
		{
			name:   "multiple children retain each change",
			raw:    `{"name":"widgets","schema":{"a":0,"b":0}}`,
			blocks: []generatedBlock{block("schema.a", `1`), block("schema.b", `2`)},
			want:   `{"name":"widgets","schema":{"a":1,"b":2}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, changed, err := rewriteManifest([]byte(test.raw), test.blocks)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("manifest = %q, want %q", got, test.want)
			}

			wantChanged := make([]string, len(test.blocks))
			for i, b := range test.blocks {
				wantChanged[i] = b.key
			}
			slices.Sort(wantChanged)
			if !slices.Equal(changed, wantChanged) {
				t.Fatalf("changed = %v, want %v", changed, wantChanged)
			}

			if _, changed, err := rewriteManifest(got, test.blocks); err != nil || len(changed) != 0 {
				t.Fatalf("repeat rewrite = %v, %v", changed, err)
			}
		})
	}

	if _, _, err := rewriteManifest([]byte(`{"schema":null}`), []generatedBlock{block("schema.owned_models", `[]`)}); err == nil {
		t.Fatal("accepted schema that is not an object")
	}
}
