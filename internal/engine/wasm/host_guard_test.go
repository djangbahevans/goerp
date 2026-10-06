package wasm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every host namespace except host.crypto must be built through
// guardedHostModule, or a webhook verifier could call it.
func TestHostNamespacesAreGuarded(t *testing.T) {
	files, err := filepath.Glob("host_*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "host_guard.go" || file == "host_crypto.go" {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(src), "NewHostModuleBuilder(") {
			t.Errorf("%s builds a host namespace without guardedHostModule", file)
		}
	}
}
