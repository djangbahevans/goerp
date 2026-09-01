package mfaverify

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The next two exercise encoding/json/v2's stricter decode defaults
// (goerp#530) through the real handler — decode happens before any
// dependency is touched in ServeHTTP, so a zero-valued Handler is enough.

func TestServeHTTP_DuplicateObjectMemberNameIsBadRequest(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest("POST", "/auth/mfa/verify", strings.NewReader(`{"mfa_token":"x","mfa_token":"y","type":"totp","code":"123456"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "malformed request body") {
		t.Errorf("body = %s, want it to mention a malformed request body", w.Body.String())
	}
}

func TestServeHTTP_InvalidUTF8IsBadRequest(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest("POST", "/auth/mfa/verify", strings.NewReader("{\"mfa_token\":\"\xff\xfe\",\"type\":\"totp\",\"code\":\"123456\"}"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "malformed request body") {
		t.Errorf("body = %s, want it to mention a malformed request body", w.Body.String())
	}
}

// encoding/json/v2's MarshalWrite doesn't escape HTML/JS-unsafe characters
// by default the way v1's Encoder did — writeJSON passes explicit options
// to keep that parity (goerp#530).
func TestWriteJSONError_EscapesHTMLUnsafeCharacters(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSONError(w, 400, "invalid_request", "<script>&</script>")

	body := w.Body.String()
	wantEscaped := "\\u003cscript\\u003e\\u0026\\u003c/script\\u003e"
	if !strings.Contains(body, wantEscaped) {
		t.Errorf("body = %s, want it to contain %s", body, wantEscaped)
	}
}
