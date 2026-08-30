package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixturePackage(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "contacts-1.3.0.erp")
	if err := os.WriteFile(path, []byte("pretend .erp package bytes"), 0o600); err != nil {
		t.Fatalf("write fixture package: %v", err)
	}
	return path
}

func TestModuleInstall_WaitCompletes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/modules/install", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_1"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/jobs/job_1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"state":"completed","output":{"module":"contacts","version":"1.3.0","succeeded_tenants":["acmecorp"],"failed_tenants":[]}},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pkgPath := writeFixturePackage(t)

	code, stdout, stderr := runCLI(t, "module", "install", pkgPath,
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	want := "installed contacts@1.3.0 (succeeded: 1 tenant(s), failed: 0 tenant(s))\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestModuleInstall_NoWaitPrintsJobID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/modules/install", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_2"},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pkgPath := writeFixturePackage(t)

	code, stdout, stderr := runCLI(t, "module", "install", pkgPath,
		"--wait=false",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "job_2") {
		t.Errorf("stdout = %q, want it to mention job_2", stdout)
	}
}

func TestModuleInstall_WaitTimeoutExitsWith124(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/modules/install", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_3"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/jobs/job_3", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"state":"running"},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pkgPath := writeFixturePackage(t)

	code, _, stderr := runCLI(t, "module", "install", pkgPath,
		"--timeout", "50ms",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 124 {
		t.Fatalf("exit code = %d, want 124 (stderr: %s)", code, stderr)
	}
}

func TestModuleInstall_DiscardedJobSurfacesCleanError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/modules/install", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_4"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/jobs/job_4", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"state":"discarded"},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pkgPath := writeFixturePackage(t)

	code, _, stderr := runCLI(t, "module", "install", pkgPath,
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "goerp jobs show job_4 --logs") {
		t.Errorf("stderr = %q, want it to point at `goerp jobs show job_4 --logs`", stderr)
	}
}

func TestModuleInstall_RegistryReferenceRejected(t *testing.T) {
	code, _, stderr := runCLI(t, "module", "install", "contacts@1.3.0",
		"--admin-url", "http://unused.invalid",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "goerp#563") {
		t.Errorf("stderr = %q, want it to reference goerp#563", stderr)
	}
}
