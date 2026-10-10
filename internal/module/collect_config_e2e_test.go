package module

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

const configFixtureMain = `package main

import (
	"time"
	"github.com/djangbahevans/goerp/sdk/go/config"
	"github.com/djangbahevans/goerp/sdk/go/config/def"
)

var (
	Country = config.String("country", "GH", config.Label("Country"),
		config.Description("Default country"), config.Category("Defaults"),
		config.FieldType("country_select"), config.Pattern("^[A-Z]{2}$"),
		config.Public(), config.RestartRequired())
	Enabled = config.Bool("enabled", false, config.Label("Enabled"))
	Batch = config.Int("batch", 50, config.Label("Batch"), config.Min(0), config.Max(100))
	Threshold = config.Float("threshold", 0.85, config.Label("Threshold"))
	Interval = def.Duration("interval", 15*time.Minute, def.Label("Interval"),
		def.MinDuration(time.Minute), def.MaxDuration(time.Hour))
	Currencies = config.StringSlice("currencies", []string{"GHS", "USD"}, config.Label("Currencies"),
		config.FieldType("multiselect"), config.Choices(config.Choice{Value: "GHS", Label: "Ghana cedi"}))
	Counts = config.IntSlice("counts", []int{1, 2}, config.Label("Counts"))
	Ratios = config.FloatSlice("ratios", []float64{0.1, 0.5}, config.Label("Ratios"))
	Empty = config.StringSlice("empty", nil, config.Label("Empty"))
	Policy = config.JSON("policy", struct { Retries int ` + "`json:\"retries\"`" + ` }{3}, config.Label("Policy"))
	Secret = config.String("secret", "", config.Label("Secret"), config.Required(), config.Encrypted(), config.Generated())
	Cert = config.Ref[string]("l10n_gh.vat_cert_number")
	OwnerInterval = def.Ref[time.Duration]("owner.interval")
	Company = config.CompanyName
)

func main() {}
`

func TestGenerate_ConfigProducesManifestBlocks(t *testing.T) {
	dir := writeCollectFixture(t, configFixtureMain, collectFixtureManifest)
	ctx := generateCtx(t)
	path := filepath.Join(dir, "manifest.json")

	result, err := Generate(ctx, dir, GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(result.Blocks, []string{"config_schema", "uses_config"}) {
		t.Errorf("Blocks = %v, want config_schema and uses_config", result.Blocks)
	}

	content := readFile(t, path)
	if !strings.Contains(content, `"version":    "1.0.0"`) || !strings.Contains(content, `"depends_on": [ "core" ]`) {
		t.Errorf("unrelated manifest values changed:\n%s", content)
	}
	var doc struct {
		ConfigSchema []manifest.ConfigEntry   `json:"config_schema"`
		UsesConfig   []manifest.UsesConfigRef `json:"uses_config"`
	}
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.ConfigSchema) != 11 {
		t.Fatalf("config_schema has %d entries, want 11", len(doc.ConfigSchema))
	}
	if !slices.IsSortedFunc(doc.ConfigSchema, func(a, b manifest.ConfigEntry) int { return strings.Compare(a.Key, b.Key) }) {
		t.Error("config_schema is not sorted by key")
	}
	entries := make(map[string]manifest.ConfigEntry, len(doc.ConfigSchema))
	for _, entry := range doc.ConfigSchema {
		entries[entry.Key] = entry
	}
	for _, test := range []struct {
		key  string
		typ  string
		want any
	}{
		{key: "country", typ: "string", want: "GH"},
		{key: "enabled", typ: "boolean", want: false},
		{key: "batch", typ: "integer", want: float64(50)},
		{key: "threshold", typ: "float", want: 0.85},
		{key: "interval", typ: "duration", want: "15m0s"},
		{key: "currencies", typ: "string[]", want: []any{"GHS", "USD"}},
		{key: "counts", typ: "integer[]", want: []any{float64(1), float64(2)}},
		{key: "ratios", typ: "float[]", want: []any{0.1, 0.5}},
		{key: "empty", typ: "string[]", want: []any{}},
		{key: "policy", typ: "json", want: map[string]any{"retries": float64(3)}},
		{key: "secret", typ: "string"},
	} {
		entry := entries[test.key]
		if entry.Type != test.typ || !reflect.DeepEqual(entry.Default, test.want) {
			t.Errorf("%s type/default = %s/%#v, want %s/%#v", test.key, entry.Type, entry.Default, test.typ, test.want)
		}
	}

	country := entries["country"]
	if country.Label != "Country" || country.Description != "Default country" || country.Category != "Defaults" || country.FieldType != "country_select" || country.ValidationRegex != "^[A-Z]{2}$" || !country.Public || !country.RestartRequired {
		t.Errorf("country metadata = %+v", country)
	}
	if batch := entries["batch"]; batch.Min != float64(0) || batch.Max != float64(100) {
		t.Errorf("batch bounds = %v..%v", batch.Min, batch.Max)
	}
	if interval := entries["interval"]; interval.Min != "1m0s" || interval.Max != "1h0m0s" {
		t.Errorf("duration bounds = %v..%v", interval.Min, interval.Max)
	}
	if currencies := entries["currencies"]; currencies.FieldType != "multiselect" || !reflect.DeepEqual(currencies.Options, []manifest.FieldOption{{Value: "GHS", Label: "Ghana cedi"}}) {
		t.Errorf("currencies options = %+v", currencies)
	}
	if secret := entries["secret"]; !secret.Required || !secret.Encrypted || !secret.Generated {
		t.Errorf("secret flags = %+v", secret)
	}
	wantRefs := []manifest.UsesConfigRef{
		{Key: "l10n_gh.vat_cert_number", Type: "string"},
		{Key: "owner.interval", Type: "duration"},
	}
	if !slices.Equal(doc.UsesConfig, wantRefs) {
		t.Errorf("uses_config = %v, want %v", doc.UsesConfig, wantRefs)
	}

	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err != nil {
		t.Fatalf("--check on generated output: %v", err)
	}
	result, err = Generate(ctx, dir, GenerateOptions{})
	if err != nil || len(result.Blocks) != 0 || len(result.Stale) != 0 {
		t.Fatalf("idempotent generation = %+v, %v", result, err)
	}

	changed := strings.NewReplacer(`"country", "GH"`, `"country", "NG"`, `Ref[string]("l10n_gh.vat_cert_number")`, `Ref[int]("l10n_gh.vat_cert_number")`).Replace(configFixtureMain)
	if err := os.WriteFile(filepath.Join(dir, "cmd", "module", "main.go"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Generate(ctx, dir, GenerateOptions{Check: true})
	if err == nil || !strings.Contains(err.Error(), "config_schema, uses_config") {
		t.Fatalf("--check after changing code = %v, want both config blocks", err)
	}
	if got := readFile(t, path); got != content {
		t.Error("--check changed the manifest")
	}

	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatal(err)
	}
	updated := readFile(t, path)
	if err := json.Unmarshal([]byte(updated), &doc); err != nil {
		t.Fatal(err)
	}
	countryIndex := slices.IndexFunc(doc.ConfigSchema, func(entry manifest.ConfigEntry) bool { return entry.Key == "country" })
	if countryIndex < 0 {
		t.Fatal("updated manifest lacks country")
	}
	if country := doc.ConfigSchema[countryIndex]; country.Default != "NG" {
		t.Errorf("updated country default = %v, want NG", country.Default)
	}
	wantRefs[0].Type = "integer"
	if !slices.Equal(doc.UsesConfig, wantRefs) {
		t.Errorf("updated uses_config = %v, want %v", doc.UsesConfig, wantRefs)
	}
	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err != nil {
		t.Fatalf("--check after updating: %v", err)
	}
}

func TestGenerate_ConfigFailuresLeaveFilesUntouched(t *testing.T) {
	for _, test := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "invalid key",
			src:  `config.String("bad.key", "", config.Label("Key"))`,
			want: "must be non-empty alphanumerics and underscores",
		},
		{
			name: "required default",
			src:  `config.String("key", "value", config.Label("Key"), config.Required())`,
			want: "default must be the zero value",
		},
		{
			name: "self reference",
			src:  `config.Ref[string]("widgets.key")`,
			want: "names the module's own key",
		},
		{
			name: "duplicate definition",
			src:  `config.String("country", "GH", config.Label("Country"))`,
			want: `config key "country" is declared more than once`,
		},
		{
			name: "duplicate reference",
			src:  `config.Ref[int]("l10n_gh.vat_cert_number")`,
			want: `config.Ref key "l10n_gh.vat_cert_number" is declared more than once`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := strings.Replace(configFixtureMain, "func main() {}", "var Extra = "+test.src+"\nfunc main() {}", 1)
			dir := writeCollectFixture(t, src, collectFixtureManifest)
			modelsDir := filepath.Join(dir, "models")
			if err := os.MkdirAll(modelsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			orphanPath := filepath.Join(modelsDir, "orphan.gen.go")
			orphan := generatedFileHeader + "package models\n"
			if err := os.WriteFile(orphanPath, []byte(orphan), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := Generate(generateCtx(t), dir, GenerateOptions{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Generate = %v, want %q", err, test.want)
			}

			if got := readFile(t, filepath.Join(dir, "manifest.json")); got != collectFixtureManifest {
				t.Error("failed generation changed manifest.json")
			}
			if got := readFile(t, orphanPath); got != orphan {
				t.Error("failed generation changed orphan model")
			}
			files, err := os.ReadDir(modelsDir)
			if err != nil || len(files) != 1 {
				t.Errorf("model files after failure = %v, %v, want only the orphan", files, err)
			}
		})
	}
}

func TestGenerate_ConfigRemovalAndWASMFalse(t *testing.T) {
	stale := strings.TrimSuffix(collectFixtureManifest, "}\n") + `, "config_schema": [{"key":"old"}], "uses_config": [{"key":"owner.old","type":"string"}]}`
	for _, wasm := range []bool{true, false} {
		t.Run(strconv.FormatBool(wasm), func(t *testing.T) {
			doc := stale
			if !wasm {
				doc = strings.Replace(doc, `"name": "widgets"`, `"name": "widgets", "wasm": false`, 1)
			}
			dir := writeCollectFixture(t, "package main\nfunc main() {}\n", doc)

			result, err := Generate(generateCtx(t), dir, GenerateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			got := readFile(t, filepath.Join(dir, "manifest.json"))
			if wasm {
				if !slices.Equal(result.Blocks, []string{"config_schema", "uses_config"}) || strings.Contains(got, "config_schema") || strings.Contains(got, "uses_config") {
					t.Errorf("empty declarations did not remove config blocks: %+v\n%s", result, got)
				}
			} else if got != doc || len(result.Blocks) != 0 {
				t.Error("wasm:false changed hand-written config blocks")
			}
		})
	}
}
