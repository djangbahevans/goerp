package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestTenantList_Success(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"tenants":[
			{"slug":"acmecorp","name":"Acme Corp","plan":"pro","status":"active","country":"GH","created_at":"2026-01-15T12:00:00Z","users":4},
			{"slug":"widgetco","name":"Widget Co","plan":"starter","status":"suspended","created_at":"2026-02-01T09:30:00Z","users":1}
		],"next_cursor":"abc123"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "list",
		"--filter", "active",
		"--plan", "pro",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}

	q, err := url.ParseQuery(strings.TrimPrefix(gotPath, "/admin/tenants?"))
	if err != nil {
		t.Fatalf("parse request query: %v", err)
	}
	if q.Get("filter") != "active" {
		t.Errorf("filter query param = %q, want %q", q.Get("filter"), "active")
	}
	if q.Get("plan") != "pro" {
		t.Errorf("plan query param = %q, want %q", q.Get("plan"), "pro")
	}

	for _, want := range []string{"SLUG", "acmecorp", "Acme Corp", "pro", "active", "GH", "2026-01-15", "4", "widgetco", "suspended"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	if !strings.Contains(stderr, "abc123") {
		t.Errorf("stderr = %q, want it to mention the next_cursor abc123", stderr)
	}
}

func TestTenantList_NoNextCursorPrintsNoHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"tenants":[]},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "list",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "SLUG") {
		t.Errorf("stdout = %q, want the table header even with zero rows", stdout)
	}
	if strings.Contains(stderr, "--cursor") {
		t.Errorf("stderr = %q, want no pagination hint when next_cursor is absent", stderr)
	}
}

func TestTenantList_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"tenants":[{"slug":"acmecorp","name":"Acme Corp","plan":"pro","status":"active","created_at":"2026-01-15T12:00:00Z","users":4}]},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "list",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
		"--json",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}

	var data struct {
		Tenants []struct {
			Slug string `json:"slug"`
		} `json:"tenants"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &data); err != nil {
		t.Fatalf("stdout %q is not the raw data envelope: %v", stdout, err)
	}
	if len(data.Tenants) != 1 || data.Tenants[0].Slug != "acmecorp" {
		t.Errorf("Tenants = %+v, want one tenant with slug acmecorp", data.Tenants)
	}
}

func TestTenantList_CursorAndLimitFlagsSetQueryParams(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"tenants":[]},"error":null}`))
	}))
	defer srv.Close()

	code, _, stderr := runCLI(t, "tenant", "list",
		"--cursor", "page2token",
		"--limit", "10",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if gotQuery.Get("cursor") != "page2token" {
		t.Errorf("cursor query param = %q, want %q", gotQuery.Get("cursor"), "page2token")
	}
	if gotQuery.Get("limit") != "10" {
		t.Errorf("limit query param = %q, want %q", gotQuery.Get("limit"), "10")
	}
}

func TestTenantList_NotFoundIsExitCode4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"not_found","message":"nope"}}`))
	}))
	defer srv.Close()

	code, _, stderr := runCLI(t, "tenant", "list",
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
