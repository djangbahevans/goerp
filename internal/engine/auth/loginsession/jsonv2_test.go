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
	WriteResponse(w, tokens, "device-1", true, IsNonBrowser(req))

	body := w.Body.String()
	wantEscaped := "\\u003cscript\\u003e\\u0026\\u003c/script\\u003e"
	if !strings.Contains(body, wantEscaped) {
		t.Errorf("body = %s, want it to contain %s", body, wantEscaped)
	}
}
