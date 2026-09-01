package mfareset

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The next two exercise encoding/json/v2's stricter decode defaults
// (goerp#530) through the real handler, once past tenant resolution and
// authentication — decode happens after those two steps in ServeHTTP, so
// this reuses the fixture's own authenticated-caller setup rather than
// re-deriving decodeJSON's behavior in isolation.

func doRawReset(t *testing.T, f *fixture, accessToken string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/users/some-id/mfa/reset", bytes.NewReader(body))
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("Authorization", "Bearer "+accessToken)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestServeHTTP_DuplicateObjectMemberNameIsBadRequest(t *testing.T) {
	f := newFixture(t)
	callerID := f.createCallerWithPassword(t)
	token := f.issueAccessToken(t, callerID)

	rec := doRawReset(t, f, token, []byte(`{"password":"a","password":"b"}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "malformed request body") {
		t.Errorf("body = %s, want it to mention a malformed request body", rec.Body.String())
	}
}

func TestServeHTTP_InvalidUTF8IsBadRequest(t *testing.T) {
	f := newFixture(t)
	callerID := f.createCallerWithPassword(t)
	token := f.issueAccessToken(t, callerID)

	rec := doRawReset(t, f, token, []byte("{\"password\":\"\xff\xfe\"}"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "malformed request body") {
		t.Errorf("body = %s, want it to mention a malformed request body", rec.Body.String())
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
