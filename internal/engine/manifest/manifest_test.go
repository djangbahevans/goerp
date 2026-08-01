package manifest

import (
	"bytes"
	"errors"
	"testing"
)

func TestLoadManifestValidMinimal(t *testing.T) {
	m := []byte(`{"name": "demo", "version": "1.0.0"}`)

	if err := LoadManifest(m); err != nil {
		t.Fatalf("expected valid minimal manifest to pass, got %v", err)
	}
}

func TestLoadManifestInvalidUtf8(t *testing.T) {
	m := []byte(`{"name": "demo`)
	m = append(m, 0xff, 0xfe) // invalid UTF-8 sequence

	err := LoadManifest(m)
	if !errors.Is(err, ErrInvalidUtf8) {
		t.Fatalf("expected ErrInvalidUtf8, got %v", err)
	}
}

func TestLoadManifestRejectsComments(t *testing.T) {
	cases := map[string][]byte{
		"line comment":  []byte("{\n  // this is a comment\n  \"name\": \"demo\"\n}"),
		"block comment": []byte("{\n  /* comment */\n  \"name\": \"demo\"\n}"),
	}

	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			err := LoadManifest(m)
			if !errors.Is(err, ErrInvalidJSON) {
				t.Fatalf("expected ErrInvalidJSON for %s, got %v", name, err)
			}
		})
	}
}

func TestLoadManifestOversized(t *testing.T) {
	// valid JSON string padded past the 1MB limit
	padding := bytes.Repeat([]byte("a"), MB)
	m := append([]byte(`{"name": "`), padding...)
	m = append(m, []byte(`"}`)...)

	err := LoadManifest(m)
	if !errors.Is(err, ErrManifestTooLarge) {
		t.Fatalf("expected ErrManifestTooLarge, got %v", err)
	}
}

func TestLoadManifestAtSizeLimit(t *testing.T) {
	// exactly 1MB of valid JSON should still be rejected — len(m) >= MB
	padding := bytes.Repeat([]byte("a"), MB-len(`{"name":""}`))
	m := append([]byte(`{"name":"`), padding...)
	m = append(m, []byte(`"}`)...)

	if len(m) != MB {
		t.Fatalf("test setup error: manifest is %d bytes, want exactly %d", len(m), MB)
	}

	err := LoadManifest(m)
	if !errors.Is(err, ErrManifestTooLarge) {
		t.Fatalf("expected a manifest at exactly the size limit to be rejected, got %v", err)
	}
}
