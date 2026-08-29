package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSchemaAccept_Success(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_1","acceptance_id":"acc_1","acceptance_ids":["acc_1"]},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "schema", "accept", "contacts",
		"--tenant", "acmecorp", "--reason", "verified manually", "--confirm", "contacts",
		"--admin-url", srv.URL, "--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(gotBody, `"module":"contacts"`) || !strings.Contains(gotBody, `"tenant":"acmecorp"`) || !strings.Contains(gotBody, `"reason":"verified manually"`) {
		t.Errorf("request body = %q, want module/tenant/reason fields", gotBody)
	}
	if !strings.Contains(stdout, "acc_1") || !strings.Contains(stdout, "job_1") {
		t.Errorf("stdout = %q, want it to mention the acceptance and job ids", stdout)
	}
}

func TestSchemaAccept_ConfirmMismatchIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "schema", "accept", "contacts",
		"--tenant", "acmecorp", "--reason", "verified manually", "--confirm", "wrong-module",
		"--admin-url", "http://unused", "--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, `must exactly match "contacts"`) {
		t.Errorf("stderr = %q, want it to mention the confirm mismatch", stderr)
	}
}

func TestSchemaAccept_MissingReasonIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "schema", "accept", "contacts",
		"--tenant", "acmecorp", "--confirm", "contacts",
		"--admin-url", "http://unused", "--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "--reason is required") {
		t.Errorf("stderr = %q, want it to mention --reason is required", stderr)
	}
}

func TestSchemaAccept_DryRunShowsBlockedWithoutAccepting(t *testing.T) {
	var acceptCalled bool
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/modules/contacts/schema", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{
			"module":"contacts","tenant":"acmecorp","version":"1.3.0",
			"blocked":[{"kind":"drop_column","table":"contacts","detail":"drops old_email","hash":"h1"}]
		},"error":null}`))
	})
	mux.HandleFunc("POST /admin/schema/accept", func(w http.ResponseWriter, r *http.Request) {
		acceptCalled = true
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_1"},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "schema", "accept", "contacts",
		"--tenant", "acmecorp", "--dry-run",
		"--admin-url", srv.URL, "--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if acceptCalled {
		t.Errorf("expected --dry-run NOT to call POST /admin/schema/accept")
	}
	if !strings.Contains(gotQuery, "verbose=true") {
		t.Errorf("request query = %q, want verbose=true so the blocked DDL detail is included", gotQuery)
	}
	if !strings.Contains(stdout, "drop_column") || !strings.Contains(stdout, "drops old_email") {
		t.Errorf("stdout = %q, want it to show the blocked DDL", stdout)
	}
}

func TestSchemaAccept_DryRunNothingBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"module":"contacts","tenant":"acmecorp","version":"1.3.0"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "schema", "accept", "contacts",
		"--tenant", "acmecorp", "--dry-run",
		"--admin-url", srv.URL, "--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "nothing currently blocked") {
		t.Errorf("stdout = %q, want it to say nothing is blocked", stdout)
	}
}
