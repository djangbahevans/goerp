package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSchemaDiff_SingleTenant(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{
			"module":"contacts","tenant":"acmecorp","version":"1.3.0",
			"safe":[{"kind":"add_column","table":"contacts"}],
			"blocked":[{"kind":"drop_column","table":"contacts","detail":"drops old_email","hash":"h1"}]
		},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "schema", "diff",
		"--tenant", "acmecorp", "--module", "contacts", "--verbose",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if gotPath != "/admin/modules/contacts/schema" {
		t.Errorf("request path = %q, want /admin/modules/contacts/schema", gotPath)
	}
	if !strings.Contains(gotQuery, "tenant=acmecorp") || !strings.Contains(gotQuery, "verbose=true") {
		t.Errorf("request query = %q, want tenant=acmecorp and verbose=true", gotQuery)
	}
	for _, want := range []string{"contacts", "1.3.0", "add_column", "drop_column", "drops old_email"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestSchemaDiff_AllFollowsPagination(t *testing.T) {
	var diffedTenants []string

	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/tenants", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"data":{"tenants":[{"slug":"acmecorp"}],"next_cursor":"page2"},"error":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"tenants":[{"slug":"globex"}]},"error":null}`))
	})
	mux.HandleFunc("GET /admin/modules/sales/schema", func(w http.ResponseWriter, r *http.Request) {
		tenant := r.URL.Query().Get("tenant")
		diffedTenants = append(diffedTenants, tenant)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"module":"sales","tenant":"` + tenant + `","version":"1.0.0"},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "schema", "diff",
		"--all", "--module", "sales",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if len(diffedTenants) != 2 || diffedTenants[0] != "acmecorp" || diffedTenants[1] != "globex" {
		t.Errorf("diffedTenants = %v, want [acmecorp globex] (pagination not followed)", diffedTenants)
	}
	if !strings.Contains(stdout, "acmecorp") || !strings.Contains(stdout, "globex") {
		t.Errorf("stdout = %q, want output for both tenants", stdout)
	}
}

func TestSchemaDiff_MissingModuleIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "schema", "diff", "--tenant", "acmecorp",
		"--admin-url", "http://unused", "--admin-token", "testtoken")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "--module is required") {
		t.Errorf("stderr = %q, want it to mention --module is required", stderr)
	}
}

func TestSchemaDiff_MissingTenantAndAllIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "schema", "diff", "--module", "contacts",
		"--admin-url", "http://unused", "--admin-token", "testtoken")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "--tenant is required") {
		t.Errorf("stderr = %q, want it to mention --tenant is required", stderr)
	}
}

func TestSchemaDiff_TenantAndAllTogetherIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "schema", "diff", "--module", "contacts", "--tenant", "acmecorp", "--all",
		"--admin-url", "http://unused", "--admin-token", "testtoken")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "mutually exclusive") {
		t.Errorf("stderr = %q, want it to mention --tenant/--all are mutually exclusive", stderr)
	}
}
