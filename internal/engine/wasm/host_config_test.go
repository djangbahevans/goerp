package wasm

import (
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func testRowKeySet(t *testing.T) *rowcrypt.RowKeySet {
	t.Helper()
	return &rowcrypt.RowKeySet{Active: rowcrypt.RowKey{KeyID: "test-key", Key: make([]byte, 32)}}
}

func newConfigTestModuleContext(moduleName string, schema []manifest.ConfigEntry) *ModuleContext {
	return NewModuleContext("req-1", moduleName, "user-1", "", nil, nil, "tenant-1", "tenant-slug", "trace-1", 0, nil, ModuleSnapshot{
		ConfigSchema: schema,
	})
}

func TestOwnConfigEntry_RejectsForeignModuleKey(t *testing.T) {
	mc := newConfigTestModuleContext("contacts", []manifest.ConfigEntry{{Key: "default_country_code", Type: "string"}})

	_, _, hostErr := ownConfigEntry(mc, "billing.default_country_code")
	if hostErr == nil {
		t.Fatal("expected an error for a key belonging to another module")
	}
	if hostErr.Code != abiv1.ErrCodeConfigKeyNotDeclared {
		t.Errorf("Code = %q, want %q", hostErr.Code, abiv1.ErrCodeConfigKeyNotDeclared)
	}
}

func TestOwnConfigEntry_RejectsUndeclaredKey(t *testing.T) {
	mc := newConfigTestModuleContext("contacts", []manifest.ConfigEntry{{Key: "default_country_code", Type: "string"}})

	_, _, hostErr := ownConfigEntry(mc, "contacts.secret_key")
	if hostErr == nil {
		t.Fatal("expected an error for an undeclared key")
	}
	if hostErr.Code != abiv1.ErrCodeConfigKeyNotDeclared {
		t.Errorf("Code = %q, want %q", hostErr.Code, abiv1.ErrCodeConfigKeyNotDeclared)
	}
}

func TestOwnConfigEntry_AcceptsOwnDeclaredKey(t *testing.T) {
	mc := newConfigTestModuleContext("contacts", []manifest.ConfigEntry{{Key: "default_country_code", Type: "string", Encrypted: false}})

	entry, subKey, hostErr := ownConfigEntry(mc, "contacts.default_country_code")
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if subKey != "default_country_code" {
		t.Errorf("subKey = %q, want %q", subKey, "default_country_code")
	}
	if entry.Type != "string" {
		t.Errorf("entry.Type = %q, want %q", entry.Type, "string")
	}
}

func TestDecodeConfigValue(t *testing.T) {
	tests := []struct {
		entryType string
		raw       string
		want      any
	}{
		{"string", "GH", "GH"},
		{"integer", "42", int64(42)},
		{"float", "3.14", 3.14},
		{"boolean", "true", true},
		{"string[]", `["GHS","USD"]`, []string{"GHS", "USD"}},
		{"integer[]", `[1,2,3]`, []int64{1, 2, 3}},
		{"float[]", `[1.5,2.5]`, []float64{1.5, 2.5}},
		{"json", `{"a":1}`, map[string]any{"a": float64(1)}},
	}
	for _, tt := range tests {
		t.Run(tt.entryType, func(t *testing.T) {
			got, err := decodeConfigValue(tt.entryType, tt.raw)
			if err != nil {
				t.Fatalf("decodeConfigValue(%q, %q) error: %v", tt.entryType, tt.raw, err)
			}
			switch want := tt.want.(type) {
			case []string:
				gotSlice, ok := got.([]string)
				if !ok || !equalSlices(gotSlice, want) {
					t.Errorf("got %#v, want %#v", got, want)
				}
			case []int64:
				gotSlice, ok := got.([]int64)
				if !ok || !equalSlices(gotSlice, want) {
					t.Errorf("got %#v, want %#v", got, want)
				}
			case []float64:
				gotSlice, ok := got.([]float64)
				if !ok || !equalSlices(gotSlice, want) {
					t.Errorf("got %#v, want %#v", got, want)
				}
			case map[string]any:
				gotMap, ok := got.(map[string]any)
				if !ok || len(gotMap) != len(want) || gotMap["a"] != want["a"] {
					t.Errorf("got %#v, want %#v", got, want)
				}
			default:
				if got != tt.want {
					t.Errorf("got %#v, want %#v", got, tt.want)
				}
			}
		})
	}
}

func TestDecodeConfigValue_InvalidReturnsError(t *testing.T) {
	if _, err := decodeConfigValue("integer", "not-a-number"); err == nil {
		t.Error("expected an error decoding a non-numeric integer value")
	}
}

func equalSlices[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEncodeConfigValue_PlainValue(t *testing.T) {
	r := &Runtime{}
	entry := manifest.ConfigEntry{Type: "string"}

	data, hostErr := encodeConfigValue(r, entry, "GH")
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if string(data) != `"GH"` {
		t.Errorf("got %s, want %q", data, `"GH"`)
	}
}

func TestEncodeConfigValue_EncryptedRequiresStringValue(t *testing.T) {
	r := &Runtime{}
	entry := manifest.ConfigEntry{Type: "string", Encrypted: true}

	_, hostErr := encodeConfigValue(r, entry, int64(42))
	if hostErr == nil {
		t.Fatal("expected an error setting an encrypted key with a non-string value")
	}
}

func TestEncodeConfigValue_EncryptedWithNoRowCryptKeysIsUnavailable(t *testing.T) {
	r := &Runtime{}
	entry := manifest.ConfigEntry{Type: "string", Encrypted: true}

	_, hostErr := encodeConfigValue(r, entry, "sk_live_secret")
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeUnavailable {
		t.Fatalf("got %v, want %s", hostErr, abiv1.ErrCodeUnavailable)
	}
}

func TestDecryptConfigValue_RoundTrips(t *testing.T) {
	keys := testRowKeySet(t)
	ciphertext, err := keys.Encrypt([]byte("sk_live_secret"))
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}

	got, hostErr := decryptConfigValue(keys, string(ciphertext))
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if got != "sk_live_secret" {
		t.Errorf("got %q, want %q", got, "sk_live_secret")
	}
}

func TestDecryptConfigValue_MalformedCiphertextPassesThroughAsPlaintext(t *testing.T) {
	keys := testRowKeySet(t)

	// A plaintext value from a tier resolveConfigQuery never encrypts —
	// an operator override or a manifest tenant_config_seeds default —
	// must be returned as-is, not rejected as a decrypt failure.
	got, hostErr := decryptConfigValue(keys, "US")
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if got != "US" {
		t.Errorf("got %q, want %q", got, "US")
	}
}

func TestDecryptConfigValue_UnknownKeyIDStillErrors(t *testing.T) {
	keys := testRowKeySet(t)

	_, hostErr := decryptConfigValue(keys, "other-key-id:bm9uY2U:Y2lwaGVydGV4dA")
	if hostErr == nil {
		t.Fatal("expected an error for ciphertext referencing an unknown key id")
	}
	if hostErr.Code != abiv1.ErrCodeConfigEncryptionError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abiv1.ErrCodeConfigEncryptionError)
	}
}

func TestEncodeConfigValue_RejectsWrongTypedValue(t *testing.T) {
	r := &Runtime{}
	entry := manifest.ConfigEntry{Type: "integer"}

	_, hostErr := encodeConfigValue(r, entry, "not-a-number")
	if hostErr == nil {
		t.Fatal("expected an error setting a string value on an integer key")
	}
}

func TestEncodeConfigValue_AcceptsMatchingType(t *testing.T) {
	r := &Runtime{}
	entry := manifest.ConfigEntry{Type: "integer"}

	data, hostErr := encodeConfigValue(r, entry, int64(42))
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if string(data) != "42" {
		t.Errorf("got %s, want %q", data, "42")
	}
}
