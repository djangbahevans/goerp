package module

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	// Stays on v1: manifest.json may be hand-authored or from an older
	// toolchain, where v2's stricter invalid-UTF-8/duplicate-key rejection
	// could newly break a module that built fine before (package.go and
	// wasmbuild.go read it under this same reasoning).
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// FrontendBuildResult describes a successful BuildFrontend call.
type FrontendBuildResult struct {
	BundlePath   string
	BundleSHA256 string
}

// BuildFrontend runs a module's frontend/ Vite project (if the module's
// manifest declares frontend.bundle: true) and writes the resulting
// bundle's sha256 into the manifest's frontend.bundle_sha256 field. It
// returns (nil, nil) when the module declares no frontend bundle, so
// callers can distinguish "nothing to build" from a build failure. When
// debug is true, the build includes source maps (cli-reference.md's
// module build --debug).
func BuildFrontend(ctx context.Context, dir string, debug bool) (*FrontendBuildResult, error) {
	manifestPath := filepath.Join(dir, "manifest.json")

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	frontend, _ := decoded["frontend"].(map[string]any)
	bundle, _ := frontend["bundle"].(bool)
	if !bundle {
		return nil, nil
	}

	frontendDir := filepath.Join(dir, "frontend")
	if info, err := os.Stat(frontendDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("frontend.bundle is true but %s does not exist", frontendDir)
	}

	// --prefer-offline skips npm's registry staleness check entirely for
	// any package already in its local cache, rather than confirming the
	// cached copy is current first — a deliberate speed/reliability
	// tradeoff (the same one many CI setups make) that can occasionally
	// resolve a semver range against an already-cached-but-not-newest
	// version instead of the latest publish. Applies to both `goerp
	// module build` and this function's own tests. Fixes the specific
	// pattern behind goerp#585: a repeat install against an unchanged
	// lockfile was paying the same live-registry cost as a first-ever
	// install for no benefit, and intermittently hanging on it.
	// --fetch-timeout/--fetch-retries/--fetch-retry-maxtimeout carry no
	// such tradeoff — they just give a single hanging or slow request a
	// fast, bounded failure and npm's own retry, instead of blocking
	// until BuildFrontend's caller (or a test's own context) times the
	// whole process out.
	npmInstallArgs := []string{
		"install",
		"--prefer-offline",
		"--fetch-timeout=60000",
		"--fetch-retries=3",
		"--fetch-retry-maxtimeout=20000",
	}
	if err := runNpm(ctx, frontendDir, npmInstallArgs...); err != nil {
		return nil, err
	}
	buildArgs := []string{"run", "build"}
	if debug {
		buildArgs = append(buildArgs, "--", "--sourcemap")
	}
	if err := runNpm(ctx, frontendDir, buildArgs...); err != nil {
		return nil, err
	}

	distDir := filepath.Join(frontendDir, "dist")
	if err := cleanStaleBundles(distDir); err != nil {
		return nil, fmt.Errorf("clean stale bundles: %w", err)
	}

	outputPath, err := findBundleOutput(distDir)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("read build output: %w", err)
	}

	fullHex := computeSHA256(data)
	bundleSHA256 := "sha256:" + fullHex

	newPath := filepath.Join(distDir, fmt.Sprintf("bundle.%s.js", fullHex[:12]))
	if err := os.Rename(outputPath, newPath); err != nil {
		return nil, fmt.Errorf("rename bundle output: %w", err)
	}

	frontend["bundle_sha256"] = bundleSHA256

	patched, err := encodeManifest(decoded)
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	if _, err := manifest.Load(patched); err != nil {
		return nil, fmt.Errorf("build produced an invalid manifest: %w", err)
	}
	if err := writeFile(manifestPath, patched); err != nil {
		return nil, err
	}

	return &FrontendBuildResult{
		BundlePath:   newPath,
		BundleSHA256: bundleSHA256,
	}, nil
}

func runNpm(ctx context.Context, dir string, args ...string) error {
	return runCmd(ctx, dir, nil, "npm", args...)
}

// runCmd runs name with args in dir, with extraEnv appended to the
// subprocess's environment, capturing combined stdout+stderr for the
// error message on failure.
func runCmd(ctx context.Context, dir string, extraEnv []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if extraEnv != nil {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s %s: %w: %s", name, strings.Join(args, " "), err, output.String())
	}

	return nil
}

// cleanStaleBundles removes bundle.<hash>.js files left over from a
// previous BuildFrontend run, so re-running against the same dir is
// idempotent — Vite only overwrites its own literal bundle.js, not a
// previously hash-renamed file.
func cleanStaleBundles(distDir string) error {
	matches, err := filepath.Glob(filepath.Join(distDir, "bundle.*.js"))
	if err != nil {
		return err
	}

	for _, m := range matches {
		if err := os.Remove(m); err != nil {
			return err
		}
	}

	return nil
}

// findBundleOutput locates the single .js file Vite's library-mode build
// produced in distDir (excluding .js.map sourcemaps).
func findBundleOutput(distDir string) (string, error) {
	entries, err := os.ReadDir(distDir)
	if err != nil {
		return "", fmt.Errorf("read dist directory: %w", err)
	}

	var matches []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".js" {
			matches = append(matches, filepath.Join(distDir, e.Name()))
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no .js output found in %s after build", distDir)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("expected exactly one .js output in %s, found %d: %v", distDir, len(matches), matches)
	}
}

func computeSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
