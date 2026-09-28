package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const codegenSchema = `{"modules": {"contacts": {"routes": [], "views": [], "load_order": 0, "models": {
  "contacts.contact": {"label_plural": "Contacts", "enabled_ops": ["get"], "fields": [{"name": "name", "type": "text", "required": true}]}
}}}}`

func TestCodegen_UsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"codegen"},
		{"codegen", "--local", "--from-engine", "--token", "k"},
		{"codegen", "--from-engine"},
		{"codegen", "--local", "--module", "contacts"},
		{"codegen", "a", "b", "--local"},
	} {
		code, _, stderr := runCLI(t, args...)
		if code != 2 || !strings.Contains(stderr, "Usage:") {
			t.Errorf("goerp %s: exit code = %d, want 2 with usage; stderr: %s", strings.Join(args, " "), code, stderr)
		}
	}
}

func TestCodegen_FromEngineWritesOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer erp_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(codegenSchema))
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name": "contacts"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	args := []string{"codegen", dir, "--from-engine", "--url", srv.URL, "--token", "erp_test"}
	code, stdout, stderr := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	out := filepath.Join(dir, "frontend", "src", "api", "generated.ts")
	if !strings.Contains(stdout, "wrote "+out) {
		t.Errorf("stdout = %q, want it to name %s", stdout, out)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(content), "getContact: (id: string): Promise<Contact>") {
		t.Errorf("output lacks getContact:\n%s", content)
	}

	if code, stdout, _ := runCLI(t, args...); code != 0 || !strings.Contains(stdout, "is up to date") {
		t.Errorf("second run: exit code %d, stdout %q; want an up-to-date report", code, stdout)
	}
}
