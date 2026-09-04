package authme

import (
	"net/http/httptest"
	"strings"
	"testing"
)

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
