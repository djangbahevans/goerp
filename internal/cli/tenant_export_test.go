package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTenantExport_WaitDownloadsAndVerifiesArchive(t *testing.T) {
	archiveBytes := []byte("pretend this is an encrypted zip archive")
	sum := sha256.Sum256(archiveBytes)
	checksum := hex.EncodeToString(sum[:])

	var mux *http.ServeMux
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
	}))
	defer srv.Close()

	var gotExportBody struct {
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
	}
	mux = http.NewServeMux()
	mux.HandleFunc("POST /admin/tenants/acmecorp/export", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotExportBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_7"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/jobs/job_7", func(w http.ResponseWriter, r *http.Request) {
		output, _ := json.Marshal(map[string]string{
			"download_url":    srv.URL + "/download/archive.zip.enc",
			"checksum_sha256": checksum,
			"decryption_key":  "test-decryption-key",
		})
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"state":"completed","output":%s},"error":null}`, output)
	})
	mux.HandleFunc("GET /download/archive.zip.enc", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiveBytes)
	})

	outputPath := filepath.Join(t.TempDir(), "acmecorp.zip")

	code, stdout, stderr := runCLI(t, "tenant", "export", "acmecorp",
		"--output", outputPath,
		"--exclude", "hr,payroll",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if len(gotExportBody.Exclude) != 2 || gotExportBody.Exclude[0] != "hr" || gotExportBody.Exclude[1] != "payroll" {
		t.Errorf("request exclude = %v, want [hr payroll]", gotExportBody.Exclude)
	}
	if !strings.Contains(stdout, "checksum verified") {
		t.Errorf("stdout = %q, want it to confirm checksum verification", stdout)
	}
	if !strings.Contains(stdout, "test-decryption-key") {
		t.Errorf("stdout = %q, want it to print the decryption key", stdout)
	}

	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read written archive: %v", err)
	}
	if string(written) != string(archiveBytes) {
		t.Errorf("written archive content mismatch")
	}
}

func TestTenantExport_NoWaitPrintsJobID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_9"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "tenant", "export", "acmecorp",
		"--output", filepath.Join(t.TempDir(), "out.zip"),
		"--wait=false",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "job_9") {
		t.Errorf("stdout = %q, want it to mention the job id", stdout)
	}
	if !strings.Contains(stdout, "jobs show job_9") {
		t.Errorf("stdout = %q, want it to mention how to check job progress", stdout)
	}
}

func TestTenantExport_MissingOutputIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "tenant", "export", "acmecorp",
		"--admin-url", "http://127.0.0.1:1",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "output") {
		t.Errorf("stderr = %q, want it to mention --output", stderr)
	}
}

func TestTenantExport_ChecksumMismatchFails(t *testing.T) {
	var mux *http.ServeMux
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
	}))
	defer srv.Close()

	mux = http.NewServeMux()
	mux.HandleFunc("POST /admin/tenants/acmecorp/export", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"job_id":"job_1"},"error":null}`))
	})
	mux.HandleFunc("GET /admin/jobs/job_1", func(w http.ResponseWriter, r *http.Request) {
		output, _ := json.Marshal(map[string]string{
			"download_url":    srv.URL + "/download/archive.zip.enc",
			"checksum_sha256": "0000000000000000000000000000000000000000000000000000000000000000",
			"decryption_key":  "test-decryption-key",
		})
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"state":"completed","output":%s},"error":null}`, output)
	})
	mux.HandleFunc("GET /download/archive.zip.enc", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("some archive bytes"))
	})

	code, _, stderr := runCLI(t, "tenant", "export", "acmecorp",
		"--output", filepath.Join(t.TempDir(), "out.zip"),
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code == 0 {
		t.Fatal("expected a non-zero exit code on checksum mismatch")
	}
	if !strings.Contains(stderr, "checksum mismatch") {
		t.Errorf("stderr = %q, want it to mention the checksum mismatch", stderr)
	}
}
