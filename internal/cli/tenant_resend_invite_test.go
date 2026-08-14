package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantResendInvite_Success(t *testing.T) {
	var gotPath, gotEmail string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct {
			Email string `json:"email"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotEmail = body.Email

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"sent"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, _ := runCLI(t, "tenant", "resend-invite", "acmecorp",
		"--email", "kwame@acmecorp.com",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if gotPath != "/admin/tenants/acmecorp/resend-invite" {
		t.Errorf("request path = %q, want /admin/tenants/acmecorp/resend-invite", gotPath)
	}
	if gotEmail != "kwame@acmecorp.com" {
		t.Errorf("request email = %q, want kwame@acmecorp.com", gotEmail)
	}
	if !strings.Contains(stdout, "invite resent") {
		t.Errorf("stdout = %q, want it to mention the invite was resent", stdout)
	}
}

func TestTenantResendInvite_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"sent"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, _ := runCLI(t, "tenant", "resend-invite", "acmecorp",
		"--email", "kwame@acmecorp.com",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
		"--json",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	var data struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &data); err != nil {
		t.Fatalf("stdout %q is not the raw data envelope: %v", stdout, err)
	}
	if data.Status != "sent" {
		t.Errorf("Status = %q, want %q", data.Status, "sent")
	}
}

func TestTenantResendInvite_NotFoundIsExitCode4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"not_found","message":"tenant not found"}}`))
	}))
	defer srv.Close()

	code, _, stderr := runCLI(t, "tenant", "resend-invite", "does-not-exist",
		"--email", "a@b.com",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 4 {
		t.Fatalf("exit code = %d, want 4 (not found, cli-reference.md §2b)", code)
	}
	if !strings.Contains(stderr, "not_found") {
		t.Errorf("stderr = %q, want it to mention the not_found API error code", stderr)
	}
}

func TestTenantResendInvite_MissingEmailIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "resend-invite", "acmecorp",
		"--admin-url", "http://127.0.0.1:1",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "email") {
		t.Errorf("stderr = %q, want it to mention the missing --email flag", stderr)
	}
}

func TestTenantResendInvite_MissingAdminTokenIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "resend-invite", "acmecorp",
		"--email", "a@b.com",
		"--admin-url", "http://127.0.0.1:1",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "admin-url") && !strings.Contains(stderr, "admin-token") {
		t.Errorf("stderr = %q, want it to mention the missing admin credential pair", stderr)
	}
}
