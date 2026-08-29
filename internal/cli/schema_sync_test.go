package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSchemaSync_ScopedNoPromptWaitsForCompletion(t *testing.T) {
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/schema/sync", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_1"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/jobs/job_1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"state":"completed","output":{"synced":[{"tenant":"acmecorp","module":"contacts"}],"failed":[]}},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "schema", "sync",
		"--tenant", "acmecorp", "--module", "contacts",
		"--admin-url", srv.URL, "--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(gotBody, `"tenant":"acmecorp"`) || !strings.Contains(gotBody, `"module":"contacts"`) {
		t.Errorf("request body = %q, want tenant/module fields", gotBody)
	}
	if !strings.Contains(stdout, "synced: 1, failed: 0") {
		t.Errorf("stdout = %q, want a synced/failed summary", stdout)
	}
}

// Piped test stdin is never a real TTY, so both cases below hit the
// noninteractive fail-fast path (isInteractiveStdin returns false)
// regardless of what's written into the pipe — accurately reflecting
// real CI/script usage (cli-reference.md §2b), which is exactly the
// scenario the fail-fast path exists for. There is no TTY available in
// this test harness, so the actual interactive y/N read path
// (confirmBroadSync) is exercised only by manual/human use of a real
// terminal, not by this suite.
func TestSchemaSync_BroadTargetNonInteractiveFailsFast(t *testing.T) {
	var statusCalled, syncCalled bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/schema/status", func(w http.ResponseWriter, r *http.Request) {
		statusCalled = true
		_, _ = w.Write([]byte(`{"data":[],"error":null}`))
	})
	mux.HandleFunc("POST /admin/schema/sync", func(w http.ResponseWriter, r *http.Request) {
		syncCalled = true
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_2"},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, _, stderr := runCLIWithStdin(t, "y\n", "schema", "sync",
		"--admin-url", srv.URL, "--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr)
	}
	if statusCalled || syncCalled {
		t.Errorf("expected neither GET /admin/schema/status nor POST /admin/schema/sync to be called when failing fast")
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("stderr = %q, want it to name --yes as the way to proceed non-interactively", stderr)
	}
}

func TestSchemaSync_BroadTargetJSONWithoutYesFailsFast(t *testing.T) {
	var syncCalled bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/schema/sync", func(w http.ResponseWriter, r *http.Request) {
		syncCalled = true
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_3"},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, _, stderr := runCLI(t, "schema", "sync",
		"--json",
		"--admin-url", srv.URL, "--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr)
	}
	if syncCalled {
		t.Errorf("expected POST /admin/schema/sync NOT to be called: --json without --yes must fail, not silently proceed")
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("stderr = %q, want it to name --yes as the way to proceed", stderr)
	}
}

func TestSchemaSync_YesFlagSkipsPrompt(t *testing.T) {
	var statusCalled, syncCalled bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/schema/status", func(w http.ResponseWriter, r *http.Request) {
		statusCalled = true
		_, _ = w.Write([]byte(`{"data":[],"error":null}`))
	})
	mux.HandleFunc("POST /admin/schema/sync", func(w http.ResponseWriter, r *http.Request) {
		syncCalled = true
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_4"},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, _, stderr := runCLI(t, "schema", "sync",
		"--yes", "--wait=false",
		"--admin-url", srv.URL, "--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if statusCalled {
		t.Errorf("expected --yes to skip the count preview (no GET /admin/schema/status call)")
	}
	if !syncCalled {
		t.Errorf("expected POST /admin/schema/sync to be called")
	}
}

func TestSchemaSync_ScheduleIgnoresWait(t *testing.T) {
	var jobsCalled bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/schema/sync", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_5"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/jobs/job_5", func(w http.ResponseWriter, r *http.Request) {
		jobsCalled = true
		_, _ = w.Write([]byte(`{"data":{"state":"scheduled"},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "schema", "sync",
		"--tenant", "acmecorp", "--schedule", "2026-05-09T02:00:00Z",
		"--admin-url", srv.URL, "--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if jobsCalled {
		t.Errorf("expected --schedule to skip polling GET /admin/jobs, since --wait default true would otherwise wait")
	}
	if !strings.Contains(stdout, "job_5") {
		t.Errorf("stdout = %q, want it to print the job id immediately", stdout)
	}
}

func TestSchemaSync_InvalidScheduleIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "schema", "sync", "--schedule", "2026-05-09T02:00:00",
		"--admin-url", "http://unused", "--admin-token", "testtoken")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "RFC 3339") {
		t.Errorf("stderr = %q, want it to mention RFC 3339", stderr)
	}
}

func TestSchemaSync_WaitTimeoutExitsWith124(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/schema/sync", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_6"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/jobs/job_6", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"state":"running"},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// A too-small --timeout would race the initial POST's own HTTP client
	// timeout (adminclient.NewFromFlags reads this same flag) rather than
	// reliably reaching the poll loop's own deadline — 50ms is small
	// enough to trigger promptly but large enough for the in-process
	// httptest POST to reliably complete first.
	code, _, stderr := runCLI(t, "schema", "sync",
		"--tenant", "acmecorp", "--timeout", "50ms",
		"--admin-url", srv.URL, "--admin-token", "testtoken",
	)

	if code != 124 {
		t.Fatalf("exit code = %d, want 124 (stderr: %s)", code, stderr)
	}
}
