package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func TestCreateScaffoldsExpectedLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo_module")

	if err := Create(dir, "demo_module", "domain", ""); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, path := range []string{
		"go.mod",
		"manifest.json",
		filepath.Join("cmd", "module", "main.go"),
		filepath.Join("translations", "en.json"),
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	if info, err := os.Stat(filepath.Join(dir, "internal")); err != nil || !info.IsDir() {
		t.Errorf("expected internal/ directory to exist")
	}
}

func TestCreateGoModUsesOrgPrefix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo_module")

	if err := Create(dir, "demo_module", "domain", "github.com/acmecorp"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	want := "module github.com/acmecorp/demo_module\n"
	if len(got) < len(want) || string(got[:len(want)]) != want {
		t.Errorf("go.mod = %q, want it to start with %q", got, want)
	}
}

// TestCreateManifestPassesRealLoader is the acceptance criterion from issue
// #26 stated as a test: `goerp module create demo` must produce a manifest
// that the actual manifest loader accepts, not just well-formed JSON.
func TestCreateManifestPassesRealLoader(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo_module")

	if err := Create(dir, "demo_module", "domain", ""); err != nil {
		t.Fatalf("Create: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}

	if _, err := manifest.Load(data); err != nil {
		t.Fatalf("scaffolded manifest failed to load: %v", err)
	}
}

func TestManifestTemplateOwnsNoModelsByDefault(t *testing.T) {
	data, err := encodeManifest(manifestTemplate("demo_module", "domain"))
	if err != nil {
		t.Fatalf("encodeManifest: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	schema, ok := decoded["schema"].(map[string]any)
	if !ok {
		t.Fatalf("schema field missing or wrong type: %#v", decoded["schema"])
	}

	if _, ok := schema["owned_models"]; !ok {
		t.Errorf("schema.owned_models key is missing (manifest-spec.md §2 requires it)")
	}
}

// TestEncodeManifestIsDeterministic guards against encoding/json/v2's
// default map key ordering, which is randomized per call (unlike v1, which
// always sorted) — without Deterministic(true), scaffolding or patching the
// same manifest twice would produce different bytes each time.
func TestEncodeManifestIsDeterministic(t *testing.T) {
	v := manifestTemplate("demo_module", "domain")

	first, err := encodeManifest(v)
	if err != nil {
		t.Fatalf("encodeManifest: %v", err)
	}

	for range 10 {
		got, err := encodeManifest(v)
		if err != nil {
			t.Fatalf("encodeManifest: %v", err)
		}
		if string(got) != string(first) {
			t.Fatalf("encodeManifest is nondeterministic:\nfirst: %s\ngot:   %s", first, got)
		}
	}
}

func TestEncodeManifestEndsWithNewline(t *testing.T) {
	data, err := encodeManifest(manifestTemplate("demo_module", "domain"))
	if err != nil {
		t.Fatalf("encodeManifest: %v", err)
	}

	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Errorf("encodeManifest output does not end with a newline: %q", data)
	}
}

func TestDisplayName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"demo_module", "Demo Module"},
		{"contacts", "Contacts"},
		{"l10n_ghana", "L10n Ghana"},
		{"a_b_c", "A B C"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := displayName(tt.name); got != tt.want {
				t.Errorf("displayName(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
