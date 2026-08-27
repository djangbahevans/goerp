package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const buildFixtureManifest = `{
  "abi_version": "1",
  "capabilities": [],
  "checksum": "sha256:empty",
  "depends_on": [],
  "description": "fixture module",
  "display_name": "Fixture",
  "engine": ">=0.1.0",
  "frontend": {
    "bundle": true
  },
  "name": "fixture",
  "schema": {
    "owned_models": []
  },
  "type": "domain",
  "version": "0.1.0"
}
`

const buildFixturePackageJSON = `{
  "name": "@goerp/module-fixture-frontend",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "build": "vite build"
  },
  "devDependencies": {
    "typescript": "7.0.2",
    "vite": "^8.2.2"
  }
}
`

const buildFixtureViteConfig = `import { defineConfig } from "vite";

export default defineConfig({
  build: {
    lib: {
      entry: "src/index.ts",
      formats: ["es"],
      fileName: () => "bundle.js",
    },
  },
});
`

const buildFixtureIndexTS = `interface PlaceholderModule {
  name: string;
}

const fixtureModule: PlaceholderModule = {
  name: "fixture",
};

export default fixtureModule;
`

func writeBuildFixture(t *testing.T, dir string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(buildFixtureManifest), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}

	frontendDir := filepath.Join(dir, "frontend")
	if err := os.MkdirAll(filepath.Join(frontendDir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir frontend/src: %v", err)
	}

	files := map[string]string{
		filepath.Join(frontendDir, "package.json"):    buildFixturePackageJSON,
		filepath.Join(frontendDir, "vite.config.ts"):  buildFixtureViteConfig,
		filepath.Join(frontendDir, "src", "index.ts"): buildFixtureIndexTS,
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

func TestModuleBuild_NoFrontendBundleIsNoop(t *testing.T) {
	dir := t.TempDir()
	manifest := strings.Replace(buildFixtureManifest, `"frontend": {
    "bundle": true
  },
  `, "", 1)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}

	code, stdout, stderr := runCLI(t, "module", "build", dir)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "nothing to build") {
		t.Errorf("stdout = %q, want it to mention there's nothing to build", stdout)
	}
}

func TestModuleBuild_MissingArgIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "module", "build")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error, cli-reference.md §2b)", code)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr = %q, want the command's usage synopsis for a usage error", stderr)
	}
}

func TestModuleBuild_MissingManifestIsNotUsageError(t *testing.T) {
	dir := t.TempDir()

	code, _, stderr := runCLI(t, "module", "build", dir)

	if code == 2 {
		t.Fatalf("exit code = 2, want a non-usage error code; stderr: %s", stderr)
	}
	if code == 0 {
		t.Fatal("expected build to fail against a directory with no manifest.json")
	}
	if strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr = %q, should not contain the usage dump for a non-usage error", stderr)
	}
}

func TestModuleBuild_Success(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not available")
	}

	dir := t.TempDir()
	writeBuildFixture(t, dir)

	code, stdout, stderr := runCLI(t, "module", "build", dir)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "built ") {
		t.Errorf("stdout = %q, want it to mention the built bundle", stdout)
	}
	if !strings.Contains(stdout, "sha256:") {
		t.Errorf("stdout = %q, want it to mention the bundle's sha256", stdout)
	}

	manifestData, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var decoded struct {
		Frontend struct {
			BundleSHA256 string `json:"bundle_sha256"`
		} `json:"frontend"`
	}
	if err := json.Unmarshal(manifestData, &decoded); err != nil {
		t.Fatalf("unmarshal manifest.json: %v", err)
	}
	if !strings.Contains(stdout, decoded.Frontend.BundleSHA256) {
		t.Errorf("manifest bundle_sha256 %q not reflected in stdout %q", decoded.Frontend.BundleSHA256, stdout)
	}
}
