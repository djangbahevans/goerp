package module

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const modelsFixtureMain = `package main

import _ "generate-fixture/schema"

func main() {}
`

const modelsFixtureManifest = `{"name":"widgets","schema":{"owned_models":[],"has_data_migrations" : true,"extends_models": [ "core.base" ]},"version" : "1.0.0"}`

func TestGenerate_OwnedModels(t *testing.T) {
	dir := writeCollectFixture(t, modelsFixtureMain, modelsFixtureManifest)
	ctx := generateCtx(t)
	path := filepath.Join(dir, "manifest.json")

	_, err := Generate(ctx, dir, GenerateOptions{Check: true})
	if err == nil || !strings.Contains(err.Error(), "schema.owned_models") {
		t.Fatalf("stale --check = %v", err)
	}
	if got := readFile(t, path); got != modelsFixtureManifest {
		t.Fatal("--check wrote the manifest")
	}
	if _, err := os.Stat(filepath.Join(dir, "models")); !os.IsNotExist(err) {
		t.Fatalf("--check created models: %v", err)
	}

	for _, test := range []struct {
		schema string
		models string
	}{
		{schema: generateFixtureSchemaOneModel, models: `["widgets.widget"]`},
		{schema: generateFixtureSchemaTwoModels, models: `["widgets.gadget","widgets.widget"]`},
		{schema: generateFixtureSchemaEmpty, models: `[]`},
	} {
		if err := os.WriteFile(filepath.Join(dir, "schema", "schema.go"), []byte(test.schema), 0o644); err != nil {
			t.Fatal(err)
		}

		result, err := Generate(ctx, dir, GenerateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(result.Blocks, []string{"schema.owned_models"}) {
			t.Fatalf("blocks = %v", result.Blocks)
		}
		want := strings.Replace(modelsFixtureManifest, `"owned_models":[]`, `"owned_models":`+test.models, 1)
		if got := readFile(t, path); got != want {
			t.Fatalf("manifest = %s, want %s", got, want)
		}

		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err != nil {
			t.Fatalf("fresh --check = %v", err)
		}
		result, err = Generate(ctx, dir, GenerateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Blocks) != 0 || !before.ModTime().Equal(after.ModTime()) {
			t.Fatal("current manifest was rewritten")
		}
	}
}

func TestGenerate_NonTableOwnedModels(t *testing.T) {
	for _, backend := range []string{`model.Virtual()`, `model.Transient(time.Minute)`} {
		t.Run(backend, func(t *testing.T) {
			schema := strings.Replace(generateFixtureSchemaOneModel, `model.Table("widgets")`, backend, 1)
			if strings.Contains(backend, "time.") {
				schema = strings.Replace(schema, `import "github.com/djangbahevans/goerp/sdk/go/model"`, "import (\"time\"; \"github.com/djangbahevans/goerp/sdk/go/model\")", 1)
			}
			moduleType := "domain"
			if backend == `model.Virtual()` {
				moduleType = "connector"
			}
			manifest := strings.Replace(modelsFixtureManifest, `"name":"widgets"`, `"name":"widgets","type":"`+moduleType+`"`, 1)
			dir := writeCollectFixture(t, modelsFixtureMain, manifest)
			if err := os.WriteFile(filepath.Join(dir, "schema", "schema.go"), []byte(schema), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := Generate(generateCtx(t), dir, GenerateOptions{}); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, filepath.Join(dir, "manifest.json")); !strings.Contains(got, `"owned_models":["widgets.widget"]`) {
				t.Fatalf("non-table model omitted: %s", got)
			}
		})
	}
}

func TestGenerate_OwnedModelsWASMFalse(t *testing.T) {
	manifest := strings.Replace(modelsFixtureManifest, `"name":"widgets"`, `"name":"widgets","wasm":false`, 1)
	manifest = strings.Replace(manifest, `"owned_models":[]`, `"owned_models":[ "manual.model" ]`, 1)
	dir := writeCollectFixture(t, "invalid module source", manifest)

	result, err := Generate(generateCtx(t), dir, GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Blocks) != 0 || readFile(t, filepath.Join(dir, "manifest.json")) != manifest {
		t.Fatal("wasm:false changed the hand-written model list")
	}
}

func TestGenerate_InvalidOwnedModelsWriteNothing(t *testing.T) {
	for _, test := range []struct {
		name string
		main string
		want string
	}{
		{
			name: "duplicate",
			main: strings.Replace(modelsFixtureMain, "func main() {}", `var duplicate = model.Define("widgets.widget")
func main() {}`, 1),
			want: "declared more than once",
		},
		{
			name: "foreign owner",
			main: strings.Replace(modelsFixtureMain, "func main() {}", `var foreign = model.Define("sales.order")
func main() {}`, 1),
			want: "module's own",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			main := strings.Replace(test.main, `import _ "generate-fixture/schema"`, "import (\n_ \"generate-fixture/schema\"\n\"github.com/djangbahevans/goerp/sdk/go/model\"\n)", 1)
			dir := writeCollectFixture(t, main, modelsFixtureManifest)

			_, err := Generate(generateCtx(t), dir, GenerateOptions{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Generate = %v, want %s", err, test.want)
			}
			if got := readFile(t, filepath.Join(dir, "manifest.json")); got != modelsFixtureManifest {
				t.Fatal("invalid declaration changed manifest")
			}
			if _, err := os.Stat(filepath.Join(dir, "models")); !os.IsNotExist(err) {
				t.Fatalf("invalid declaration wrote models: %v", err)
			}
		})
	}
}
