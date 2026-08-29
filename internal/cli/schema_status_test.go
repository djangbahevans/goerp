package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSchemaStatus_Success(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"tenant":"acmecorp","module_name":"contacts","current_version":"1.0.0","status":"ok","synced_at":"2026-01-15T12:05:00Z","data_migration_version":"1.0.0","data_migration_status":"ok"},
			{"tenant":"acmecorp","module_name":"sales","current_version":"2.0.0","status":"failed"}
		],"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "schema", "status",
		"--tenant", "acmecorp",
		"--filter", "ok",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(gotQuery, "tenant=acmecorp") || !strings.Contains(gotQuery, "filter=ok") {
		t.Errorf("request query = %q, want tenant=acmecorp and filter=ok", gotQuery)
	}
	for _, want := range []string{"contacts", "1.0.0", "acmecorp", "ok", "sales", "2.0.0", "failed", "-"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestSchemaStatus_TenantAllOmitsQueryParam(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"error":null}`))
	}))
	defer srv.Close()

	code, _, stderr := runCLI(t, "schema", "status",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if strings.Contains(gotQuery, "tenant=") {
		t.Errorf("request query = %q, want no tenant param for the default \"all\"", gotQuery)
	}
}

func TestSchemaStatus_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"tenant":"acmecorp","module_name":"contacts","current_version":"1.0.0","status":"ok"}],"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "schema", "status",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
		"--json",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, `"tenant":"acmecorp"`) {
		t.Errorf("stdout = %q, want raw JSON envelope data", stdout)
	}
}
