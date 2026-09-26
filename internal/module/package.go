package module

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// PackageOptions configures Package.
type PackageOptions struct {
	// Output overrides the default <dir>/build/<name>-<version>.erp path.
	// A relative Output is relative to the working directory, not dir.
	Output                 string
	SkipWasm, SkipFrontend bool
	Debug                  bool
}

// PackageResult describes a successful Package call.
type PackageResult struct {
	ArchivePath   string
	SidecarPath   string
	ArchiveSHA256 string
}

// Package compiles a module (WASM binary and frontend bundle, unless
// skipped) and assembles a real .erp package: a zip archive containing
// manifest.json, module.wasm, the frontend bundle, the backend
// translations/ and the frontend/translations/ files, plus a
// sha256sum-format sidecar file for the archive itself.
func Package(ctx context.Context, dir string, opts PackageOptions) (*PackageResult, error) {
	manifestPath := filepath.Join(dir, "manifest.json")

	name, version, err := readNameVersion(manifestPath)
	if err != nil {
		return nil, err
	}

	if !opts.SkipFrontend {
		if _, err := BuildFrontend(ctx, dir, opts.Debug); err != nil {
			return nil, err
		}
	}
	if !opts.SkipWasm {
		if _, err := BuildWasm(ctx, dir, opts.Debug); err != nil {
			return nil, err
		}
	}

	manifestBytes, err := packagedManifest(dir, manifestPath)
	if err != nil {
		return nil, err
	}

	outputPath := opts.Output
	if outputPath == "" {
		outputPath = filepath.Join(dir, "build", fmt.Sprintf("%s-%s.erp", name, version))
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	if err := writeArchive(outputPath, dir, manifestBytes); err != nil {
		_ = os.Remove(outputPath)
		return nil, err
	}

	archiveData, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	archiveHex := computeSHA256(archiveData)

	sidecarPath := outputPath + ".sha256"
	sidecar := fmt.Sprintf("%s  %s\n", archiveHex, filepath.Base(outputPath))
	if err := writeFile(sidecarPath, []byte(sidecar)); err != nil {
		return nil, err
	}

	return &PackageResult{
		ArchivePath:   outputPath,
		SidecarPath:   sidecarPath,
		ArchiveSHA256: "sha256:" + archiveHex,
	}, nil
}

func readNameVersion(manifestPath string) (name, version string, err error) {
	decoded, err := readManifestJSON(manifestPath)
	if err != nil {
		return "", "", err
	}

	name, _ = decoded["name"].(string)
	version, _ = decoded["version"].(string)
	if name == "" || version == "" {
		return "", "", fmt.Errorf("manifest is missing name/version")
	}

	return name, version, nil
}

// packagedManifest is the source manifest with checksum and
// frontend.bundle_sha256 set from the module.wasm and bundle that
// writeArchive packages, so the .erp's manifest always matches its own
// contents. The source manifest.json is never written: the hashes are
// build outputs.
func packagedManifest(dir, manifestPath string) ([]byte, error) {
	decoded, err := readManifestJSON(manifestPath)
	if err != nil {
		return nil, err
	}

	hashed := false
	wasmData, err := os.ReadFile(filepath.Join(dir, "module.wasm"))
	switch {
	case err == nil:
		decoded["checksum"] = "sha256:" + computeSHA256(wasmData)
		hashed = true
	case !os.IsNotExist(err):
		return nil, fmt.Errorf("read module.wasm: %w", err)
	}

	frontend, _ := decoded["frontend"].(map[string]any)
	if bundle, _ := frontend["bundle"].(bool); bundle {
		matches, err := filepath.Glob(filepath.Join(dir, "frontend", "dist", "bundle.*.js"))
		if err != nil {
			return nil, err
		}
		switch len(matches) {
		case 0:
		case 1:
			data, err := os.ReadFile(matches[0])
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", matches[0], err)
			}
			frontend["bundle_sha256"] = "sha256:" + computeSHA256(data)
			hashed = true
		default:
			return nil, fmt.Errorf("frontend/dist has %d bundle.*.js files; rebuild the frontend so exactly one remains", len(matches))
		}
	}

	packaged, err := encodeManifest(decoded)
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	if hashed {
		if _, err := manifest.Load(packaged); err != nil {
			return nil, fmt.Errorf("build produced an invalid manifest: %w", err)
		}
	}
	return packaged, nil
}

func writeArchive(outputPath, dir string, manifestBytes []byte) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)

	if err := addZipEntry(w, "manifest.json", manifestBytes); err != nil {
		return err
	}

	wasmPath := filepath.Join(dir, "module.wasm")
	if data, err := os.ReadFile(wasmPath); err == nil {
		if err := addZipEntry(w, "module.wasm", data); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read module.wasm: %w", err)
	}

	bundleMatches, err := filepath.Glob(filepath.Join(dir, "frontend", "dist", "bundle.*.js"))
	if err != nil {
		return err
	}
	for _, m := range bundleMatches {
		data, err := os.ReadFile(m)
		if err != nil {
			return fmt.Errorf("read %s: %w", m, err)
		}
		if err := addZipEntry(w, filepath.Join("frontend", "dist", filepath.Base(m)), data); err != nil {
			return err
		}
	}

	translationMatches, err := filepath.Glob(filepath.Join(dir, "translations", "*.json"))
	if err != nil {
		return err
	}
	for _, m := range translationMatches {
		data, err := os.ReadFile(m)
		if err != nil {
			return fmt.Errorf("read %s: %w", m, err)
		}
		if err := addZipEntry(w, filepath.Join("translations", filepath.Base(m)), data); err != nil {
			return err
		}
	}

	// Checked here with the rules the engine applies when it loads the
	// package, so a bad file fails the build instead of the install.
	frontendDir := filepath.FromSlash(l10n.FrontendTranslationsDir)
	frontendMatches, err := filepath.Glob(filepath.Join(dir, frontendDir, "*.json"))
	if err != nil {
		return err
	}
	for _, m := range frontendMatches {
		data, err := os.ReadFile(m)
		if err != nil {
			return fmt.Errorf("read %s: %w", m, err)
		}
		if err := l10n.ValidateFrontendTranslation(strings.TrimSuffix(filepath.Base(m), ".json"), data); err != nil {
			return err
		}
		if err := addZipEntry(w, filepath.Join(frontendDir, filepath.Base(m)), data); err != nil {
			return err
		}
	}

	return w.Close()
}

func addZipEntry(w *zip.Writer, name string, data []byte) error {
	entry, err := w.Create(filepath.ToSlash(name))
	if err != nil {
		return fmt.Errorf("create archive entry %s: %w", name, err)
	}
	if _, err := io.Copy(entry, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("write archive entry %s: %w", name, err)
	}

	return nil
}
