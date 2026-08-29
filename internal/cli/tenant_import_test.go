package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestTenantImport_WaitTimeoutExitsWith124 guards goerp#478's fix:
// tenant import's wait-timeout path used to return a plain error (exit 1)
// instead of cli-reference.md §2b's documented exit 124 —
// adminclient.WaitForJob (shared with schema sync, which already got this
// right) fixes that for import too.
func TestTenantImport_WaitTimeoutExitsWith124(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/tenants/import/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"input_ref":"staged/archive.enc"},"error":null}`))
	})
	mux.HandleFunc("POST /admin/tenants/import", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_11"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/jobs/job_11", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"state":"running"},"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	archivePath := filepath.Join(t.TempDir(), "archive.enc")
	if err := os.WriteFile(archivePath, []byte("pretend encrypted archive bytes"), 0o600); err != nil {
		t.Fatalf("write fixture archive: %v", err)
	}

	code, _, stderr := runCLI(t, "tenant", "import", "acmecorp",
		"--input", archivePath,
		"--decryption-key", "dGVzdC1rZXk",
		"--confirm", "acmecorp",
		"--wait-timeout", "50ms",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 124 {
		t.Fatalf("exit code = %d, want 124 (stderr: %s)", code, stderr)
	}
}
