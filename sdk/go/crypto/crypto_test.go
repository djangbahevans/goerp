package crypto

import "testing"

// A malformed hex signature is rejected before any host call: the non-wasip1
// import stub panics, so reaching it would fail these tests.
func TestVerifyHMACHex_MalformedSignatureIsFalseWithoutHostCall(t *testing.T) {
	for _, sigHex := range []string{"zz", "abc", "0g", " ab", "ab "} {
		if VerifyHMACSHA256Hex([]byte("key"), []byte("data"), sigHex) {
			t.Errorf("VerifyHMACSHA256Hex(%q) = true, want false", sigHex)
		}
		if VerifyHMACSHA512Hex([]byte("key"), []byte("data"), sigHex) {
			t.Errorf("VerifyHMACSHA512Hex(%q) = true, want false", sigHex)
		}
	}
}
