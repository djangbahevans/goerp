package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// encoding/json/v2's MarshalWrite doesn't escape HTML/JS-unsafe characters
// by default the way v1's Encoder did — writeAuthError passes explicit
// options to keep that parity (goerp#529).
func TestWriteAuthError_EscapesHTMLUnsafeCharacters(t *testing.T) {
	w := httptest.NewRecorder()
	writeAuthError(w, 401, gatewayAuthFailedCode, "<script>&</script>")

	body := w.Body.String()
	wantEscaped := "\\u003cscript\\u003e\\u0026\\u003c/script\\u003e"
	if !strings.Contains(body, wantEscaped) {
		t.Errorf("body = %s, want it to contain %s", body, wantEscaped)
	}
}

func TestWriteAuthError_EscapesJSLineAndParagraphSeparators(t *testing.T) {
	lineSep := string(rune(0x2028)) // U+2028 LINE SEPARATOR
	paraSep := string(rune(0x2029)) // U+2029 PARAGRAPH SEPARATOR

	w := httptest.NewRecorder()
	writeAuthError(w, 401, gatewayAuthFailedCode, "before"+lineSep+"after"+paraSep+"end")

	body := w.Body.String()
	wantEscaped := "before\\u2028after\\u2029end"
	if !strings.Contains(body, wantEscaped) {
		t.Errorf("body = %s, want it to contain %s", body, wantEscaped)
	}
}
