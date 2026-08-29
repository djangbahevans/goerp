package tenantexport

import (
	"encoding/json"
	"testing"
)

func TestDecryptOutput_TenantExportKindDecryptsKeyOnly(t *testing.T) {
	keys := testRowKeySet(t)

	plaintextKey := "plain-archive-key"
	encryptedKey, err := keys.Encrypt([]byte(plaintextKey))
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}

	stored := Result{DownloadURL: "https://example/archive.zip.enc", Checksum: "abc123", DecryptionKey: string(encryptedKey)}
	storedJSON, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal stored result: %v", err)
	}

	got, err := DecryptOutput(keys, "tenant.export", storedJSON)
	if err != nil {
		t.Fatalf("DecryptOutput() error: %v", err)
	}

	var decrypted Result
	if err := json.Unmarshal(got, &decrypted); err != nil {
		t.Fatalf("unmarshal decrypted output: %v", err)
	}
	if decrypted.DecryptionKey != plaintextKey {
		t.Errorf("DecryptionKey = %q, want %q", decrypted.DecryptionKey, plaintextKey)
	}
	if decrypted.DownloadURL != stored.DownloadURL || decrypted.Checksum != stored.Checksum {
		t.Errorf("other fields changed: got %+v, want download_url/checksum from %+v", decrypted, stored)
	}
}

func TestDecryptOutput_OtherKindReturnsOutputUnchanged(t *testing.T) {
	keys := testRowKeySet(t)

	raw := json.RawMessage(`{"anything": "goes here, not even valid tenant.export shape"}`)
	got, err := DecryptOutput(keys, "tenant.offboard_immediate", raw)
	if err != nil {
		t.Fatalf("DecryptOutput() error: %v", err)
	}
	if string(got) != string(raw) {
		t.Errorf("output changed for a non-tenant.export kind: got %s, want %s", got, raw)
	}
}

func TestDecryptOutput_WrongKeyErrors(t *testing.T) {
	keys := testRowKeySet(t)
	otherKeys := testRowKeySet(t)

	encryptedKey, err := otherKeys.Encrypt([]byte("plain-archive-key"))
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}
	stored := Result{DecryptionKey: string(encryptedKey)}
	storedJSON, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal stored result: %v", err)
	}

	if _, err := DecryptOutput(keys, "tenant.export", storedJSON); err == nil {
		t.Fatal("DecryptOutput() error = nil, want an error for a key encrypted under a different RowKeySet")
	}
}
