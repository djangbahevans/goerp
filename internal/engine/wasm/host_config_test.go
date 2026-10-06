package wasm

import (
	"encoding/json/v2"
	"reflect"
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

func TestOwnConfigEntry_RejectsQualifiedKey(t *testing.T) {
	mc := newConfigTestModuleContext("contacts", []manifest.ConfigEntry{{Key: "default_country_code", Type: "string"}})

	_, _, hostErr := ownConfigEntry(mc, "contacts.default_country_code")
	if hostErr == nil {
		t.Fatal("expected an error for a key belonging to another module")
	}
	if hostErr.Code != abiv1.ErrCodeConfigKeyNotDeclared {
		t.Errorf("Code = %q, want %q", hostErr.Code, abiv1.ErrCodeConfigKeyNotDeclared)
	}
}

func TestOwnConfigEntry_RejectsUndeclaredKey(t *testing.T) {
	mc := newConfigTestModuleContext("contacts", []manifest.ConfigEntry{{Key: "default_country_code", Type: "string"}})

	_, _, hostErr := ownConfigEntry(mc, "secret_key")
	if hostErr == nil {
		t.Fatal("expected an error for an undeclared key")
	}
	if hostErr.Code != abiv1.ErrCodeConfigKeyNotDeclared {
		t.Errorf("Code = %q, want %q", hostErr.Code, abiv1.ErrCodeConfigKeyNotDeclared)
	}
}

func TestOwnConfigEntry_AcceptsOwnDeclaredKey(t *testing.T) {
	mc := newConfigTestModuleContext("contacts", []manifest.ConfigEntry{{Key: "default_country_code", Type: "string", Encrypted: false}})

	entry, qualified, hostErr := ownConfigEntry(mc, "default_country_code")
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if qualified != "contacts.default_country_code" {
		t.Errorf("qualified = %q, want %q", qualified, "contacts.default_country_code")
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

func TestEncodeConfigValue_EncryptedValidatesDeclaredType(t *testing.T) {
	r := &Runtime{rowCryptKeys: testRowKeySet(t)}

	tests := []struct {
		name  string
		entry manifest.ConfigEntry
		value any
	}{
		{"string given a number", manifest.ConfigEntry{Type: "string", Encrypted: true}, int64(42)},
		{"integer given a string", manifest.ConfigEntry{Type: "integer", Encrypted: true}, "42"},
		{"integer given a fraction", manifest.ConfigEntry{Type: "integer", Encrypted: true}, 3.7},
		{"string array given numbers", manifest.ConfigEntry{Type: "string[]", Encrypted: true}, []any{int64(1), int64(2)}},
		{"duration that does not parse", manifest.ConfigEntry{Type: "duration", Encrypted: true}, "soon"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, hostErr := encodeConfigValue(r, tt.entry, tt.value)
			if hostErr == nil || hostErr.Code != abiv1.ErrCodeDeserializeError {
				t.Fatalf("got %v, want %s", hostErr, abiv1.ErrCodeDeserializeError)
			}
		})
	}
}

func TestEncodeConfigValue_EncryptedRoundTripsNonStringTypes(t *testing.T) {
	keys := testRowKeySet(t)
	r := &Runtime{rowCryptKeys: keys}

	tests := []struct {
		name      string
		entryType string
		value     any
		want      any
	}{
		{"integer", "integer", int64(42), int64(42)},
		{"float", "float", 0.85, 0.85},
		{"boolean", "boolean", true, true},
		{"duration", "duration", "15m", "15m"},
		{"string array", "string[]", []any{"a", "b"}, []string{"a", "b"}},
		{"json", "json", map[string]any{"k": "v"}, map[string]any{"k": "v"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, hostErr := encodeConfigValue(r, manifest.ConfigEntry{Type: tt.entryType, Encrypted: true}, tt.value)
			if hostErr != nil {
				t.Fatalf("encode: %v", hostErr)
			}
			var ciphertext string
			if err := json.Unmarshal(data, &ciphertext); err != nil {
				t.Fatalf("stored value is not a JSON string: %v", err)
			}
			plaintext, hostErr := decryptConfigValue(keys, ciphertext)
			if hostErr != nil {
				t.Fatalf("decrypt: %v", hostErr)
			}
			got, err := decodeConfigValue(tt.entryType, plaintext)
			if err != nil {
				t.Fatalf("decode %q: %v", plaintext, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("round trip = %#v, want %#v", got, tt.want)
			}
		})
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

func TestDecryptConfigValue_MalformedCiphertextErrors(t *testing.T) {
	keys := testRowKeySet(t)

	_, hostErr := decryptConfigValue(keys, "US")
	if hostErr == nil {
		t.Fatal("expected an error for a value that isn't ciphertext")
	}
	if hostErr.Code != abiv1.ErrCodeConfigEncryptionError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abiv1.ErrCodeConfigEncryptionError)
	}
}

func TestDecryptConfigValue_UnknownKeyIDErrors(t *testing.T) {
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

func TestEncodeConfigValue_DurationMustParse(t *testing.T) {
	r := &Runtime{}
	entry := manifest.ConfigEntry{Type: "duration"}

	data, hostErr := encodeConfigValue(r, entry, "15m")
	if hostErr != nil {
		t.Fatalf("unexpected error: %v", hostErr)
	}
	if string(data) != `"15m"` {
		t.Errorf("got %s, want %q", data, `"15m"`)
	}

	for _, bad := range []any{"soon", int64(900)} {
		if _, hostErr := encodeConfigValue(r, entry, bad); hostErr == nil {
			t.Errorf("expected an error for duration value %v", bad)
		}
	}
}
