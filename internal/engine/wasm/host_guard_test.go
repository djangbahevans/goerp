package wasm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostNamespacesAreGuarded(t *testing.T) {
	files, err := filepath.Glob("host_*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "host_guard.go" {
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
