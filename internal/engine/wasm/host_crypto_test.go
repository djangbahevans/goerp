package wasm

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"hash"
	"testing"
)

func TestHMACHash(t *testing.T) {
	for _, algo := range []string{"sha256", "sha512"} {
		if _, ok := hmacHash(algo); !ok {
			t.Errorf("hmacHash(%q) unsupported, want supported", algo)
		}
	}
	for _, algo := range []string{"", "md5", "SHA256", "sha1", "blake2b"} {
		if _, ok := hmacHash(algo); ok {
			t.Errorf("hmacHash(%q) supported, want rejected", algo)
		}
	}
}

func TestVerifyHMAC(t *testing.T) {
	sign := func(newHash func() hash.Hash, key, data []byte) []byte {
		mac := hmac.New(newHash, key)
		mac.Write(data)
		return mac.Sum(nil)
	}
	key, data := []byte("key"), []byte("payload")
	good := sign(sha256.New, key, data)

	tests := []struct {
		name string
		hash func() hash.Hash
		key  []byte
		data []byte
		sig  []byte
		want bool
	}{
		{"matching signature", sha256.New, key, data, good, true},
		{"sha512 matching signature", sha512.New, key, data, sign(sha512.New, key, data), true},
		{"wrong key", sha256.New, []byte("other"), data, good, false},
		{"tampered data", sha256.New, key, []byte("payloae"), good, false},
		{"algorithm mismatch", sha512.New, key, data, good, false},
		{"truncated signature", sha256.New, key, data, good[:len(good)-1], false},
		{"extended signature", sha256.New, key, data, append(bytes.Clone(good), 0), false},
		{"empty signature", sha256.New, key, data, nil, false},
		{"empty key and data", sha256.New, nil, nil, sign(sha256.New, nil, nil), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verifyHMAC(tt.hash, tt.key, tt.data, tt.sig); got != tt.want {
				t.Errorf("verifyHMAC = %v, want %v", got, tt.want)
			}
		})
	}
}
