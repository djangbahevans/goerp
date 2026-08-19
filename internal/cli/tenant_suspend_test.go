package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantSuspend_Success(t *testing.T) {
	var gotPath, gotReason string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotReason = body.Reason

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","status":"suspended"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, _ := runCLI(t, "tenant", "suspend", "acmecorp",
		"--reason", "Unpaid invoice #INV-2026-0042",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if gotPath != "/admin/tenants/acmecorp/suspend" {
		t.Errorf("request path = %q, want /admin/tenants/acmecorp/suspend", gotPath)
	}
	if gotReason != "Unpaid invoice #INV-2026-0042" {
		t.Errorf("request reason = %q, want %q", gotReason, "Unpaid invoice #INV-2026-0042")
	}
	if !strings.Contains(stdout, "suspended") {
		t.Errorf("stdout = %q, want it to mention the tenant was suspended", stdout)
	}
}

func TestTenantSuspend_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","status":"suspended"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, _ := runCLI(t, "tenant", "suspend", "acmecorp",
		"--reason", "Security incident investigation",
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
	if data.Status != "suspended" {
		t.Errorf("Status = %q, want %q", data.Status, "suspended")
	}
}

func TestTenantSuspend_NotFoundIsExitCode4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"not_found","message":"tenant not found"}}`))
	}))
	defer srv.Close()

	code, _, stderr := runCLI(t, "tenant", "suspend", "does-not-exist",
		"--reason", "test",
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

func TestTenantSuspend_MissingReasonIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "suspend", "acmecorp",
		"--admin-url", "http://127.0.0.1:1",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "reason") {
		t.Errorf("stderr = %q, want it to mention the missing --reason flag", stderr)
	}
}

func TestTenantSuspend_MissingAdminTokenIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "suspend", "acmecorp",
		"--reason", "test",
		"--admin-url", "http://127.0.0.1:1",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "admin-url") && !strings.Contains(stderr, "admin-token") {
		t.Errorf("stderr = %q, want it to mention the missing admin credential pair", stderr)
	}
}
