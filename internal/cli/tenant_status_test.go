package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantStatus_Success(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{
			"slug":"acmecorp","name":"Acme Corp","plan":"pro","status":"active","region":"eu-west-1",
			"country":"GH","created_at":"2026-01-15T12:00:00Z",
			"schema_table_count":42,"modules_synced":2,"modules_total":3,
			"modules":[
				{"module_name":"contacts","current_version":"1.0.0","status":"ok","synced_at":"2026-01-15T12:05:00Z"},
				{"module_name":"sales","current_version":"2.0.0","status":"ok","synced_at":"2026-01-15T12:06:00Z"},
				{"module_name":"hr","current_version":"1.0.0","status":"failed"}
			],
			"provisioning_duration":"5.2s",
			"admin_user":{"id":"u1","email":"admin@acmecorp.com"}
		},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "status", "acmecorp",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if gotPath != "/admin/tenants/acmecorp" {
		t.Errorf("request path = %q, want /admin/tenants/acmecorp", gotPath)
	}

	for _, want := range []string{
		"Acme Corp", "pro", "active", "eu-west-1", "GH", "2026-01-15",
		"42", "2/3", "5.2s", "admin@acmecorp.com", "u1",
		"contacts", "1.0.0", "sales", "2.0.0", "hr", "failed",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestTenantStatus_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"acmecorp","name":"Acme Corp","plan":"pro","status":"active","region":"default","created_at":"2026-01-15T12:00:00Z","modules_synced":0,"modules_total":0},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "status", "acmecorp",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
		"--json",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}

	var data struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &data); err != nil {
		t.Fatalf("stdout %q is not the raw data envelope: %v", stdout, err)
	}
	if data.Slug != "acmecorp" {
		t.Errorf("Slug = %q, want %q", data.Slug, "acmecorp")
	}
}

func TestTenantStatus_NoAdminUserOrModulesOmitsThoseSections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"freshco","name":"Fresh Co","plan":"starter","status":"provisioning","region":"default","created_at":"2026-01-15T12:00:00Z","modules_synced":0,"modules_total":0},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "status", "freshco",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if strings.Contains(stdout, "Admin user:") {
		t.Errorf("stdout = %q, want no Admin user line when admin_user is absent", stdout)
	}
	if strings.Contains(stdout, "MODULE") {
		t.Errorf("stdout = %q, want no module table when modules is absent", stdout)
	}
	if strings.Contains(stdout, "Provisioning duration:") {
		t.Errorf("stdout = %q, want no provisioning duration line when absent", stdout)
	}
}

func TestTenantStatus_UnknownSlugIsExitCode4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"not_found","message":"tenant not found"}}`))
	}))
	defer srv.Close()

	code, _, stderr := runCLI(t, "tenant", "status", "does-not-exist",
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

func TestTenantStatus_MissingSlugArgIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "status",
		"--admin-url", "http://127.0.0.1:1",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if stderr == "" {
		t.Error("expected a usage error message on stderr")
	}
}
