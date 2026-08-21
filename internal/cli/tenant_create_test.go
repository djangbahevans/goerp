package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTenantCreate_NoWaitSuccess(t *testing.T) {
	var gotPath string
	var gotBody struct {
		Slug       string `json:"slug"`
		Name       string `json:"name"`
		AdminEmail string `json:"admin_email"`
		AdminName  string `json:"admin_name"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","workflow_id":"provision-tenant-acmecorp"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "create", "acmecorp",
		"--admin-email", "admin@acmecorp.com",
		"--wait=false",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if gotPath != "/admin/tenants" {
		t.Errorf("request path = %q, want /admin/tenants", gotPath)
	}
	if gotBody.Slug != "acmecorp" {
		t.Errorf("request slug = %q, want %q", gotBody.Slug, "acmecorp")
	}
	if gotBody.Name != "acmecorp" {
		t.Errorf("request name = %q, want %q (defaulted to slug)", gotBody.Name, "acmecorp")
	}
	if gotBody.AdminEmail != "admin@acmecorp.com" {
		t.Errorf("request admin_email = %q, want %q", gotBody.AdminEmail, "admin@acmecorp.com")
	}
	if !strings.Contains(stdout, "provisioning started") {
		t.Errorf("stdout = %q, want it to mention provisioning started", stdout)
	}
	if !strings.Contains(stdout, "provision-tenant-acmecorp") {
		t.Errorf("stdout = %q, want it to mention the workflow id", stdout)
	}
}

func TestTenantCreate_CustomNamePassedThrough(t *testing.T) {
	var gotName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotName = body.Name
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","workflow_id":"provision-tenant-acmecorp"},"error":null}`))
	}))
	defer srv.Close()

	code, _, stderr := runCLI(t, "tenant", "create", "acmecorp",
		"--name", "Acme Corporation",
		"--admin-email", "admin@acmecorp.com",
		"--wait=false",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if gotName != "Acme Corporation" {
		t.Errorf("request name = %q, want %q", gotName, "Acme Corporation")
	}
}

func TestTenantCreate_WaitPollsUntilActive(t *testing.T) {
	var statusCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/tenants", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","workflow_id":"provision-tenant-acmecorp"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/tenants/acmecorp", func(w http.ResponseWriter, r *http.Request) {
		n := statusCalls.Add(1)
		status := "provisioning"
		if n >= 3 {
			status = "active"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":{"slug":"acmecorp","name":"acmecorp","plan":"starter","status":%q,"region":"default","created_at":"2026-01-01T00:00:00Z","modules_synced":0,"modules_total":0},"error":null}`, status)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "create", "acmecorp",
		"--admin-email", "admin@acmecorp.com",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if statusCalls.Load() < 3 {
		t.Errorf("status endpoint called %d times, want at least 3", statusCalls.Load())
	}
	if !strings.Contains(stdout, "is active") {
		t.Errorf("stdout = %q, want it to report the tenant is active", stdout)
	}
	if !strings.Contains(stderr, "still provisioning") {
		t.Errorf("stderr = %q, want progress lines while waiting", stderr)
	}
}

func TestTenantCreate_WaitTimesOut(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/tenants", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","workflow_id":"provision-tenant-acmecorp"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/tenants/acmecorp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","name":"acmecorp","plan":"starter","status":"provisioning","region":"default","created_at":"2026-01-01T00:00:00Z","modules_synced":0,"modules_total":0},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, _, stderr := runCLI(t, "tenant", "create", "acmecorp",
		"--admin-email", "admin@acmecorp.com",
		"--wait-timeout", "1ms",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero (should time out)")
	}
	if !strings.Contains(stderr, "provisioning") {
		t.Errorf("stderr = %q, want it to mention the tenant is still provisioning", stderr)
	}
}

func TestTenantCreate_JSONOutputReflectsFinalStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/tenants", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","workflow_id":"provision-tenant-acmecorp"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/tenants/acmecorp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","name":"acmecorp","plan":"starter","status":"active","region":"default","created_at":"2026-01-01T00:00:00Z","modules_synced":0,"modules_total":0},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "create", "acmecorp",
		"--admin-email", "admin@acmecorp.com",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
		"--json",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
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

func TestTenantCreate_MissingAdminEmailIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "create", "acmecorp",
		"--admin-url", "http://127.0.0.1:1",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "admin-email") {
		t.Errorf("stderr = %q, want it to mention --admin-email", stderr)
	}
}

func TestTenantCreate_MissingAdminTokenIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "create", "acmecorp",
		"--admin-email", "admin@acmecorp.com",
		"--admin-url", "http://127.0.0.1:1",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "admin-url") && !strings.Contains(stderr, "admin-token") {
		t.Errorf("stderr = %q, want it to mention the missing admin credential pair", stderr)
	}
}

func TestTenantCreate_NotFoundDuringPollIsExitCode4(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/tenants", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","workflow_id":"provision-tenant-acmecorp"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/tenants/acmecorp", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"not_found","message":"tenant not found"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, _, stderr := runCLI(t, "tenant", "create", "acmecorp",
		"--admin-email", "admin@acmecorp.com",
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
