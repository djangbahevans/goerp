package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantUnsuspend_Success(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","status":"active"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, _ := runCLI(t, "tenant", "unsuspend", "acmecorp",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if gotPath != "/admin/tenants/acmecorp/unsuspend" {
		t.Errorf("request path = %q, want /admin/tenants/acmecorp/unsuspend", gotPath)
	}
	if !strings.Contains(stdout, "unsuspended") {
		t.Errorf("stdout = %q, want it to mention the tenant was unsuspended", stdout)
	}
}

func TestTenantUnsuspend_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","status":"active"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, _ := runCLI(t, "tenant", "unsuspend", "acmecorp",
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
	if data.Status != "active" {
		t.Errorf("Status = %q, want %q", data.Status, "active")
	}
}

func TestTenantUnsuspend_NotFoundIsExitCode4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"not_found","message":"tenant not found"}}`))
	}))
	defer srv.Close()

	code, _, stderr := runCLI(t, "tenant", "unsuspend", "does-not-exist",
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

func TestTenantUnsuspend_MissingAdminTokenIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "unsuspend", "acmecorp",
		"--admin-url", "http://127.0.0.1:1",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "admin-url") && !strings.Contains(stderr, "admin-token") {
		t.Errorf("stderr = %q, want it to mention the missing admin credential pair", stderr)
	}
}
