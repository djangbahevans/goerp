package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminOperatorsIssueCert_WritesFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/operators/issue-cert" {
			t.Errorf("request path = %q, want /admin/operators/issue-cert", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"certificate":"cert-pem","private_key":"key-pem","serial_number":"11:22:33"},"error":null}`))
	}))
	defer srv.Close()

	dir := t.TempDir()

	code, stdout, stderr := runCLI(t, "admin", "operators", "issue-cert",
		"--name", "kwame-operator",
		"--expires", "30d",
		"--output", dir,
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "kwame-operator") || !strings.Contains(stdout, "11:22:33") {
		t.Errorf("stdout = %q, want it to mention the name and serial", stdout)
	}

	certPath := filepath.Join(dir, "kwame-operator.crt")
	keyPath := filepath.Join(dir, "kwame-operator.key")

	certBytes, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert file: %v", err)
	}
	if string(certBytes) != "cert-pem" {
		t.Errorf("cert file contents = %q, want %q", certBytes, "cert-pem")
	}

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if string(keyBytes) != "key-pem" {
		t.Errorf("key file contents = %q, want %q", keyBytes, "key-pem")
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestAdminOperatorsIssueCert_MissingNameIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "admin", "operators", "issue-cert",
		"--admin-url", "http://127.0.0.1:1",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "name") {
		t.Errorf("stderr = %q, want it to mention the missing --name flag", stderr)
	}
}

func TestAdminOperatorsIssueCert_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"certificate":"cert-pem","private_key":"key-pem","serial_number":"11:22:33"},"error":null}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	code, stdout, stderr := runCLI(t, "admin", "operators", "issue-cert",
		"--name", "kwame-operator",
		"--output", dir,
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
		"--json",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}

	var data struct {
		SerialNumber string `json:"serial_number"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &data); err != nil {
		t.Fatalf("stdout %q is not the raw data envelope: %v", stdout, err)
	}
	if data.SerialNumber != "11:22:33" {
		t.Errorf("SerialNumber = %q, want %q", data.SerialNumber, "11:22:33")
	}
}

func TestAdminOperatorsRevokeCert_Success(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"name":"ci-deploy","serial_number":"11:22:33","status":"revoked"},"error":null}`))
	}))
	defer srv.Close()

	code, stdout, stderr := runCLI(t, "admin", "operators", "revoke-cert", "ci-deploy",
		"--confirm", "ci-deploy",
		"--admin-url", srv.URL,
		"--admin-token", "testtoken",
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "ci-deploy") {
		t.Errorf("stdout = %q, want it to mention ci-deploy", stdout)
	}
	if !strings.Contains(gotBody, "ci-deploy") {
		t.Errorf("request body = %q, want it to contain the name", gotBody)
	}
}

func TestAdminOperatorsRevokeCert_WrongConfirmationIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "admin", "operators", "revoke-cert", "ci-deploy",
		"--confirm", "wrong-name",
		"--admin-url", "http://127.0.0.1:1",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "confirm") {
		t.Errorf("stderr = %q, want it to mention --confirm", stderr)
	}
}

func TestAdminOperatorsRevokeCert_MissingConfirmationIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "admin", "operators", "revoke-cert", "ci-deploy",
		"--admin-url", "http://127.0.0.1:1",
		"--admin-token", "testtoken",
	)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr, "confirm") {
		t.Errorf("stderr = %q, want it to mention --confirm", stderr)
	}
}

func TestAdminOperatorsRevokeCert_NotFoundIsExitCode4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"not_found","message":"no live certificate found for that name"}}`))
	}))
	defer srv.Close()

	code, _, stderr := runCLI(t, "admin", "operators", "revoke-cert", "does-not-exist",
		"--confirm", "does-not-exist",
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
