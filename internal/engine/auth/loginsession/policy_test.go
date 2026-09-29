package loginsession

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
)

func TestWriteResponse_ReportsPasswordPolicyResult(t *testing.T) {
	deadline := time.Date(2026, 10, 13, 9, 30, 0, 0, time.UTC)
	cases := []struct {
		name   string
		policy password.Result
		want   map[string]any
	}{
		{"passed", password.Result{}, map[string]any{}},
		{"nudge", password.Result{Outcome: password.UpdateRecommended}, map[string]any{"password_update_recommended": true}},
		{"nudge with deadline", password.Result{Outcome: password.UpdateRecommended, Deadline: &deadline},
			map[string]any{"password_update_recommended": true, "password_update_deadline": "2026-10-13T09:30:00Z"}},
		{"required", password.Result{Outcome: password.ChangeRequired}, map[string]any{"password_change_required": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
			WriteResponse(w, &authtoken.Tokens{AccessToken: "a", RefreshToken: "r", ExpiresIn: 900}, "device-1", false, IsNonBrowser(req), tc.policy)

			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			for _, key := range []string{"password_update_recommended", "password_update_deadline", "password_change_required"} {
				if body[key] != tc.want[key] {
					t.Errorf("%s = %v, want %v; body = %s", key, body[key], tc.want[key], w.Body.String())
				}
			}
		})
	}
}
