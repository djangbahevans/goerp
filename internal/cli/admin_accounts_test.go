package cli

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminAccountsRequests(t *testing.T) {
	for _, tc := range []struct {
		action, method, suffix string
		flags                  []string
	}{
		{"suspend", http.MethodPost, "/suspend", []string{"--reason", "compromised"}},
		{"unsuspend", http.MethodPost, "/unsuspend", nil},
		{"delete", http.MethodDelete, "", []string{"--confirm", "user-1"}},
		{"mfa-reset", http.MethodPost, "/mfa/reset", []string{"--confirm", "user-1"}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != "/admin/accounts/user-1"+tc.suffix || r.URL.RawQuery != "" {
					t.Errorf("request = %s %s", r.Method, r.URL)
				}

				if r.Header.Get("Authorization") != "Bearer testtoken" {
					t.Error("missing operator credential")
				}

				if tc.action == "suspend" {
					var body struct {
						Reason string `json:"reason"`
					}
					if err := json.UnmarshalRead(r.Body, &body); err != nil || body.Reason != "compromised" {
						t.Errorf("reason = %q, err=%v", body.Reason, err)
					}
				}

				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			args := append([]string{"admin", "accounts", tc.action, "user-1"}, tc.flags...)
			args = append(args, "--admin-url", srv.URL, "--admin-token", "testtoken", "--json")
			code, stdout, stderr := runCLI(t, args...)
			if code != 0 || strings.TrimSpace(stdout) != "null" || calls != 1 {
				t.Fatalf("exit=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, calls)
			}
		})
	}
}

func TestAdminAccountsConfirmationAndReason(t *testing.T) {
	for _, args := range [][]string{
		{"suspend", "user-1"},
		{"suspend", "user-1", "--reason", " "},
		{"delete", "user-1"},
		{"delete", "user-1", "--confirm", "user-2"},
		{"mfa-reset", "user-1"},
		{"mfa-reset", "user-1", "--confirm", "user-2"},
		{"delete", "user-1", "--dry-run"},
		{"mfa-reset", "user-1", "--dry-run"},
		{"mfa-reset", "user-1", "--dry-run", "--confirm", "user-2"},
	} {
		code, _, stderr := runCLI(t, append([]string{"admin", "accounts"}, args...)...)
		if code != 2 || (!strings.Contains(stderr, "reason") && !strings.Contains(stderr, "confirm")) {
			t.Errorf("args=%v exit=%d stderr=%q", args, code, stderr)
		}
	}
}

func TestAdminAccountsDryRun(t *testing.T) {
	for _, action := range []string{"delete", "mfa-reset"} {
		t.Run(action, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantMethod := http.MethodDelete
				wantPath := "/admin/accounts/user-1"
				if action == "mfa-reset" {
					wantMethod = http.MethodPost
					wantPath += "/mfa/reset"
				}

				if r.Method != wantMethod || r.URL.Path != wantPath || r.URL.Query().Get("dry_run") != "true" {
					t.Errorf("dry-run request = %s %s", r.Method, r.URL)
				}

				_, _ = w.Write([]byte(`{"data":{"email":"person@example.test","tenants":["alpha","beta"],"live_sessions":3},"error":null}`))
			}))
			defer srv.Close()

			args := []string{"admin", "accounts", action, "user-1", "--confirm", "user-1", "--dry-run", "--admin-url", srv.URL, "--admin-token", "testtoken"}
			code, stdout, stderr := runCLI(t, args...)
			if code != 0 || !strings.Contains(stdout, "alpha, beta") || !strings.Contains(stdout, "live sessions: 3") || !strings.Contains(stdout, "person@example.test") {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}

			code, stdout, stderr = runCLI(t, append(args, "--json")...)
			var preview struct {
				LiveSessions int `json:"live_sessions"`
			}
			if err := json.Unmarshal([]byte(stdout), &preview); err != nil || code != 0 || preview.LiveSessions != 3 {
				t.Fatalf("exit=%d stdout=%q stderr=%q err=%v", code, stdout, stderr, err)
			}
		})
	}
}

func TestAdminAccountsAPIErrors(t *testing.T) {
	for _, tc := range []struct{ status, exit int }{{401, 3}, {404, 4}, {409, 5}, {500, 1}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"data":null,"error":{"code":"account_error","message":"account change failed"}}`))
		}))

		code, stdout, stderr := runCLI(t, "admin", "accounts", "unsuspend", "user-1", "--admin-url", srv.URL, "--admin-token", "testtoken", "--json")
		srv.Close()
		if code != tc.exit || !strings.Contains(stdout, "account_error") || !strings.Contains(stderr, "account_error") {
			t.Errorf("status=%d exit=%d stdout=%q stderr=%q", tc.status, code, stdout, stderr)
		}
	}
}
