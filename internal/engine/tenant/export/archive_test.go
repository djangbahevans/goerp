package tenantexport

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestBuildArchive_ContainsManifestAndModuleFiles(t *testing.T) {
	m := manifest{
		TenantID:   "tenant-1",
		TenantSlug: "acme",
		ExportedAt: time.Now().UTC(),
		Modules: []manifestModule{
			{Name: "contacts", Version: "1.0.0", File: "contacts.jsonl"},
		},
	}
	moduleData := map[string][]byte{"contacts": []byte(`{"model":"contact","record":{"id":"1"}}` + "\n")}

	data, err := buildArchive(m, moduleData)
	if err != nil {
		t.Fatalf("buildArchive() error: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open built archive as zip: %v", err)
	}

	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["manifest.json"] {
		t.Error("archive missing manifest.json")
	}
	if !names["contacts.jsonl"] {
		t.Error("archive missing contacts.jsonl")
	}

	mf, err := zr.Open("manifest.json")
	if err != nil {
		t.Fatalf("open manifest.json: %v", err)
	}
	defer mf.Close()
	var decoded manifest
	if err := json.NewDecoder(mf).Decode(&decoded); err != nil {
		t.Fatalf("decode manifest.json: %v", err)
	}
	if decoded.TenantSlug != "acme" {
		t.Errorf("manifest TenantSlug = %q, want %q", decoded.TenantSlug, "acme")
	}
}

func TestBuildArchive_MissingModuleDataErrors(t *testing.T) {
	m := manifest{Modules: []manifestModule{{Name: "contacts", File: "contacts.jsonl"}}}
	if _, err := buildArchive(m, map[string][]byte{}); err == nil {
		t.Fatal("expected an error for missing module data")
	}
}

func TestEncryptArchive_RoundTrips(t *testing.T) {
	plaintext := []byte("this is the plaintext archive content")

	ciphertext, keyB64, err := encryptArchive(plaintext)
	if err != nil {
		t.Fatalf("encryptArchive() error: %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext equals plaintext — not actually encrypted")
	}

	key, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		t.Fatalf("ciphertext too short to contain a nonce: %d bytes", len(ciphertext))
	}
	nonce, sealed := ciphertext[:nonceSize], ciphertext[nonceSize:]
	decrypted, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		t.Fatalf("gcm.Open: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptArchive_DifferentKeysPerCall(t *testing.T) {
	_, key1, err := encryptArchive([]byte("a"))
	if err != nil {
		t.Fatalf("encryptArchive() error: %v", err)
	}
	_, key2, err := encryptArchive([]byte("a"))
	if err != nil {
		t.Fatalf("encryptArchive() error: %v", err)
	}
	if key1 == key2 {
		t.Error("expected a fresh key per export, got the same key twice")
	}
}

func TestChecksumHex_IsDeterministicAndDependsOnContent(t *testing.T) {
	a := checksumHex([]byte("hello"))
	b := checksumHex([]byte("hello"))
	c := checksumHex([]byte("world"))
	if a != b {
		t.Errorf("checksumHex not deterministic: %q != %q", a, b)
	}
	if a == c {
		t.Error("checksumHex produced the same digest for different content")
	}
}
