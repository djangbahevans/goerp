package httpx

import (
	"context"
	"strings"
	"testing"
)

// encoding/json/v2's MarshalWrite doesn't escape HTML/JS-unsafe characters
// by default the way v1's Encoder did — writeJSON passes explicit options
// to keep that parity (goerp#531).
func TestHandleHealth_EscapesHTMLUnsafeCharacters(t *testing.T) {
	s := newTestServer()
	s.SetHealthFn(func(ctx context.Context) HealthReport {
		return HealthReport{
			Status: "degraded",
			Checks: map[string]CheckResult{
				"postgres_primary": {Status: "ok"},
				"meilisearch":      {Status: "error", Error: "<script>&</script>"},
			},
		}
	})

	w := doRequest(t, s, "GET", "/_health")

	wantEscaped := "\\u003cscript\\u003e\\u0026\\u003c/script\\u003e"
	if !strings.Contains(w.Body.String(), wantEscaped) {
		t.Errorf("body = %s, want it to contain %s", w.Body.String(), wantEscaped)
	}
}
