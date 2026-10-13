package cli

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestTenantImport_ReportsAccountCounts(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", jsonOutput), func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("POST /admin/tenants/import/upload", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"input_ref":"staged/archive.enc"},"error":null}`))
			})
			mux.HandleFunc("POST /admin/tenants/import", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"job_id":"job_11"},"error":null}`))
			})
			mux.HandleFunc("GET /admin/jobs/job_11", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"state":"completed","output":{"tenant_id":"target","tenant_slug":"acmecorp","modules_imported":["contacts"],"accounts_matched":2,"accounts_created":1}},"error":null}`))
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			archivePath := filepath.Join(t.TempDir(), "archive.enc")
			if err := os.WriteFile(archivePath, []byte("encrypted archive"), 0o600); err != nil {
				t.Fatal(err)
			}

			args := []string{"tenant", "import", "acmecorp", "--input", archivePath, "--decryption-key", "key", "--confirm", "acmecorp", "--admin-url", server.URL, "--admin-token", "testtoken"}
			if jsonOutput {
				args = append(args, "--json")
			}
			code, stdout, stderr := runCLI(t, args...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			if jsonOutput {
				var result struct {
					Matched int `json:"accounts_matched"`
					Created int `json:"accounts_created"`
				}
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatal(err)
				}
				if result.Matched != 2 || result.Created != 1 {
					t.Fatalf("output %s", stdout)
				}
			} else if !strings.Contains(stdout, "accounts: 2 matched, 1 created") {
				t.Fatalf("output %s", stdout)
			}
		})
	}
}
