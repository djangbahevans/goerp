package configvalue

import (
	"encoding/json/v2"
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func testKeys() *rowcrypt.RowKeySet {
	return &rowcrypt.RowKeySet{Active: rowcrypt.RowKey{KeyID: "test-key", Key: make([]byte, 32)}}
}

func TestEncode_PlainValueIsItsJSON(t *testing.T) {
	data, err := Encode(manifest.ConfigEntry{Key: "n", Type: "integer"}, int64(7), nil)
	if err != nil || string(data) != "7" {
		t.Errorf("Encode() = %s, %v; want 7", data, err)
	}
}

func TestEncode_EncryptedValueRoundTrips(t *testing.T) {
	keys := testKeys()
	entry := manifest.ConfigEntry{Key: "token", Type: "string", Encrypted: true}

	data, err := Encode(entry, "sk_live_secret", keys)
	if err != nil {
		t.Fatalf("Encode() error: %v", err)
	}
	var ciphertext string
	if err := json.Unmarshal(data, &ciphertext); err != nil {
		t.Fatalf("stored value is not a JSON string: %v", err)
	}
	if ciphertext == "sk_live_secret" {
		t.Fatal("the value was stored in plaintext")
	}
	plaintext, err := keys.Decrypt([]byte(ciphertext))
	if err != nil || string(plaintext) != "sk_live_secret" {
		t.Errorf("Decrypt() = %q, %v; want the original value", plaintext, err)
	}
}

func TestEncode_Errors(t *testing.T) {
	encrypted := manifest.ConfigEntry{Key: "token", Type: "string", Encrypted: true}

	_, err := Encode(manifest.ConfigEntry{Key: "n", Type: "integer"}, "seven", nil)
	if _, invalid := errors.AsType[*InvalidValueError](err); !invalid {
		t.Errorf("wrong type: error = %v, want *InvalidValueError", err)
	}
	if _, err := Encode(manifest.ConfigEntry{Key: "n", Type: "integer", Max: float64(5)}, int64(9), nil); err == nil {
		t.Error("a value above the maximum was accepted")
	}
	if _, err := Encode(encrypted, "x", nil); !errors.Is(err, ErrNoEncryptionKey) {
		t.Errorf("encrypted without keys: error = %v, want ErrNoEncryptionKey", err)
	}
	if _, err := Encode(manifest.ConfigEntry{Key: "d", Type: "duration"}, "soon", nil); err == nil {
		t.Error("a malformed duration was accepted")
	}
}
