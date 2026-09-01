package manifest

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestLoadManifestFrontendOmitted(t *testing.T) {
	fields := minimalManifestFields()
	// No "frontend" key at all -- the common backend-only case
	// (manifest-spec.md §2: absent for backend-only modules).

	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	got, err := Load(m)
	if err != nil {
		t.Fatalf("expected manifest without a frontend key to load, got %v", err)
	}
	if got.Frontend != nil {
		t.Fatalf("expected Frontend to be nil, got %#v", got.Frontend)
	}
}

func TestLoadManifestFrontendBundleValidation(t *testing.T) {
	validHash := "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	cases := map[string]struct {
		frontend map[string]any
		wantErr  bool
	}{
		"bundle false, no hash": {
			frontend: map[string]any{"bundle": false},
			wantErr:  false,
		},
		"bundle true, valid hash": {
			frontend: map[string]any{"bundle": true, "bundle_sha256": validHash},
			wantErr:  false,
		},
		"bundle true, missing hash": {
			frontend: map[string]any{"bundle": true},
			wantErr:  true,
		},
		"bundle true, malformed hash (no sha256: prefix)": {
			frontend: map[string]any{"bundle": true, "bundle_sha256": "not-a-real-hash"},
			wantErr:  true,
		},
		"bundle true, malformed hash (wrong length)": {
			frontend: map[string]any{"bundle": true, "bundle_sha256": "sha256:deadbeef"},
			wantErr:  true,
		},
		"bundle true, malformed hash (non-hex characters)": {
			frontend: map[string]any{
				"bundle":        true,
				"bundle_sha256": "sha256:g3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85g",
			},
			wantErr: true,
		},
		"bundle false, malformed hash still rejected": {
			frontend: map[string]any{"bundle": false, "bundle_sha256": "not-a-real-hash"},
			wantErr:  true,
		},
		"bundle missing": {
			frontend: map[string]any{},
			wantErr:  true,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fields := minimalManifestFields()
			fields["frontend"] = c.frontend

			m, err := json.Marshal(fields)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}

			_, err = Load(m)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got none")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestLoadManifestFrontendEntryDefault(t *testing.T) {
	fields := minimalManifestFields()
	fields["frontend"] = map[string]any{"bundle": false}

	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	got, err := Load(m)
	if err != nil {
		t.Fatalf("expected valid manifest to load, got %v", err)
	}
	if got.Frontend == nil {
		t.Fatalf("expected Frontend to be non-nil")
	}
	if got.Frontend.Entry != "frontend/src/index.ts" {
		t.Fatalf("expected default entry path, got %q", got.Frontend.Entry)
	}
}

func TestLoadManifestFrontendEntryExplicit(t *testing.T) {
	fields := minimalManifestFields()
	fields["frontend"] = map[string]any{"bundle": false, "entry": "frontend/src/custom.ts"}

	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	got, err := Load(m)
	if err != nil {
		t.Fatalf("expected valid manifest to load, got %v", err)
	}
	if got.Frontend.Entry != "frontend/src/custom.ts" {
		t.Fatalf("expected explicit entry path to be preserved, got %q", got.Frontend.Entry)
	}
}

func TestLoadManifestFrontendMissingHashErrorReferencesField(t *testing.T) {
	fields := minimalManifestFields()
	fields["frontend"] = map[string]any{"bundle": true}

	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	_, err = Load(m)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "BundleSHA256") {
		t.Fatalf("expected error to reference BundleSHA256, got %v", err)
	}
}

func TestLoadManifestFrontendMalformedHashErrorIsTranslated(t *testing.T) {
	fields := minimalManifestFields()
	fields["frontend"] = map[string]any{"bundle": true, "bundle_sha256": "not-a-real-hash"}

	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	_, err = Load(m)
	if err == nil {
		t.Fatalf("expected error")
	}

	want := "BundleSHA256 must be a sha256:-prefixed 64-character hex hash"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected translated error %q, got %v", want, err)
	}
}
