package module

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func requireNpm(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not available")
	}
}

const fixtureManifest = `{
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

const fixturePackageJSON = `{
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

const fixtureViteConfig = `import { defineConfig } from "vite";

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

const fixtureTsconfig = `{
  "compilerOptions": {
    "target": "ES2023",
    "lib": ["ES2023", "DOM"],
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "types": ["vite/client"],
    "strict": true,
    "noEmit": true,
    "skipLibCheck": true
  },
  "include": ["src"]
}
`

const fixtureIndexTS = `interface PlaceholderModule {
  name: string;
}

const fixtureModule: PlaceholderModule = {
  name: "fixture",
};

export default fixtureModule;
`

const fixtureIndexTSBroken = `export { default } from "./does-not-exist";
`

func writeMinimalFrontendFixture(t *testing.T, dir string, indexTS string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(fixtureManifest), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}

	frontendDir := filepath.Join(dir, "frontend")
	if err := os.MkdirAll(filepath.Join(frontendDir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir frontend/src: %v", err)
	}

	files := map[string]string{
		filepath.Join(frontendDir, "package.json"):    fixturePackageJSON,
		filepath.Join(frontendDir, "vite.config.ts"):  fixtureViteConfig,
		filepath.Join(frontendDir, "tsconfig.json"):   fixtureTsconfig,
		filepath.Join(frontendDir, "src", "index.ts"): indexTS,
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

func TestBuildFrontendProducesHashedBundleAndManifest(t *testing.T) {
	requireNpm(t)

	dir := t.TempDir()
	writeMinimalFrontendFixture(t, dir, fixtureIndexTS)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := BuildFrontend(ctx, dir)
	if err != nil {
		t.Fatalf("BuildFrontend: %v", err)
	}
	if result == nil {
		t.Fatal("BuildFrontend returned nil result for a module with frontend.bundle: true")
	}

	if !regexp.MustCompile(`bundle\.[0-9a-f]{12}\.js$`).MatchString(result.BundlePath) {
		t.Errorf("BundlePath = %q, want to match bundle.<12-hex>.js", result.BundlePath)
	}

	data, err := os.ReadFile(result.BundlePath)
	if err != nil {
		t.Fatalf("read bundle output: %v", err)
	}
	sum := sha256.Sum256(data)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if result.BundleSHA256 != want {
		t.Errorf("BundleSHA256 = %q, want %q", result.BundleSHA256, want)
	}

	manifestData, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(manifestData, &decoded); err != nil {
		t.Fatalf("unmarshal manifest.json: %v", err)
	}
	frontend, _ := decoded["frontend"].(map[string]any)
	if frontend["bundle_sha256"] != want {
		t.Errorf("manifest frontend.bundle_sha256 = %v, want %q", frontend["bundle_sha256"], want)
	}

	if _, err := manifest.Load(manifestData); err != nil {
		t.Errorf("patched manifest failed to load: %v", err)
	}
}

func TestBuildFrontendIsIdempotent(t *testing.T) {
	requireNpm(t)

	dir := t.TempDir()
	writeMinimalFrontendFixture(t, dir, fixtureIndexTS)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	first, err := BuildFrontend(ctx, dir)
	if err != nil {
		t.Fatalf("first BuildFrontend: %v", err)
	}

	second, err := BuildFrontend(ctx, dir)
	if err != nil {
		t.Fatalf("second BuildFrontend: %v", err)
	}

	if first.BundleSHA256 != second.BundleSHA256 {
		t.Errorf("hash changed across identical builds: %q vs %q", first.BundleSHA256, second.BundleSHA256)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "frontend", "dist", "bundle.*.js"))
	if err != nil {
		t.Fatalf("glob dist: %v", err)
	}
	if len(matches) != 1 {
		t.Errorf("expected exactly one bundle.*.js in dist after two builds, found %d: %v", len(matches), matches)
	}
}

func TestBuildFrontendNoopWhenNoFrontendBundle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{
  "abi_version": "1",
  "capabilities": [],
  "checksum": "sha256:empty",
  "depends_on": [],
  "description": "fixture module",
  "display_name": "Fixture",
  "engine": ">=0.1.0",
  "name": "fixture",
  "schema": {
    "owned_models": []
  },
  "type": "domain",
  "version": "0.1.0"
}
`), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}

	result, err := BuildFrontend(context.Background(), dir)
	if err != nil {
		t.Fatalf("BuildFrontend: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for a module with no frontend bundle, got %+v", result)
	}
}

func TestBuildFrontendErrorsOnBuildFailure(t *testing.T) {
	requireNpm(t)

	dir := t.TempDir()
	writeMinimalFrontendFixture(t, dir, fixtureIndexTSBroken)

	before, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json before build: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := BuildFrontend(ctx, dir)
	if err == nil {
		t.Fatalf("expected BuildFrontend to fail on a broken entry point, got result %+v", result)
	}
	if result != nil {
		t.Errorf("expected nil result on failure, got %+v", result)
	}

	after, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json after build: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("manifest.json was modified despite build failure")
	}
}
