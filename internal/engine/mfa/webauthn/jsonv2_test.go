package webauthn

import (
	"context"
	"strings"
	"testing"
)

// encoding/json/v2's Marshal doesn't escape HTML/JS-unsafe characters by
// default the way v1's did — BeginRegistration/BeginLogin pass explicit
// options to keep that parity for optionsJSON, which a future caller
// writes straight to an HTTP response (goerp#530).
func TestBeginRegistration_EscapesHTMLUnsafeAccountName(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	optionsJSON, _, err := env.service.BeginRegistration(context.Background(), userID, "<script>&alice</script>")
	if err != nil {
		t.Fatalf("BeginRegistration() error: %v", err)
	}

	body := string(optionsJSON)
	wantEscaped := "\\u003cscript\\u003e\\u0026alice\\u003c/script\\u003e"
	if !strings.Contains(body, wantEscaped) {
		t.Errorf("optionsJSON = %s, want it to contain %s", body, wantEscaped)
	}
}

// BeginLogin's own CredentialAssertion options carry no accountName-derived
// field (WebAuthn login assertions don't include user identity), so there's
// no unsafe-character field to demonstrate the same escaping through — its
// json.Marshal call still gets the same options as BeginRegistration's for
// consistency, just with nothing in this call's actual output to exercise.
