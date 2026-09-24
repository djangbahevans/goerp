package loginsession

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
)

// encoding/json/v2's MarshalWrite doesn't escape HTML/JS-unsafe characters
// by default the way v1's Encoder did — writeJSON passes explicit options
// to keep that parity (goerp#530).
func TestWriteResponse_NonBrowser_EscapesHTMLUnsafeCharacters(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.Header.Set("X-Client-Type", "cli")

	tokens := &authtoken.Tokens{AccessToken: "<script>&</script>", RefreshToken: "r", ExpiresIn: 900}
	WriteResponse(w, tokens, "device-1", true, IsNonBrowser(req), false)

	body := w.Body.String()
	wantEscaped := "\\u003cscript\\u003e\\u0026\\u003c/script\\u003e"
	if !strings.Contains(body, wantEscaped) {
		t.Errorf("body = %s, want it to contain %s", body, wantEscaped)
	}
}

func TestWriteReissuedAccessToken_NonBrowser_EscapesHTMLUnsafeCharacters(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/mfa/reverify", nil)
	req.Header.Set("X-Client-Type", "cli")
	WriteReissuedAccessToken(w, req, "<script>&</script>", 900, true, nil)

	body := w.Body.String()
	wantEscaped := "\\u003cscript\\u003e\\u0026\\u003c/script\\u003e"
	if !strings.Contains(body, wantEscaped) {
		t.Errorf("body = %s, want it to contain %s", body, wantEscaped)
	}
}

func TestWriteReissuedAccessToken_Browser_SetsOnlyAccessCookieAndKeepsBodyFields(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/mfa/enroll/totp/confirm", nil)
	WriteReissuedAccessToken(w, req, "tok", 900, false, map[string]any{"recovery_codes": nil})

	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-access_token" || cookies[0].MaxAge != 0 {
		t.Fatalf("cookies = %+v, want one non-persistent __Host-access_token", cookies)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"recovery_codes":null`) || !strings.Contains(body, `"expires_in":900`) || strings.Contains(body, "tok") {
		t.Errorf("body = %s, want recovery_codes null, expires_in, and no token", body)
	}
}
