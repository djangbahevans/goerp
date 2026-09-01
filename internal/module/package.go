package module

import (
	"archive/zip"
	"bytes"
	"context"
	// Stays on v1 — see build.go's identical import comment.
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// PackageOptions configures Package.
type PackageOptions struct {
	// Output overrides the default build/<name>-<version>.erp path
	// (relative to dir).
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
// manifest.json, module.wasm, the frontend bundle, and translations, plus
// a sha256sum-format sidecar file for the archive itself.
func Package(ctx context.Context, dir string, opts PackageOptions) (*PackageResult, error) {
	manifestPath := filepath.Join(dir, "manifest.json")

	name, version, err := readNameVersion(manifestPath)
	if err != nil {
		return nil, err
	}

	// Frontend runs first: it only ever touches frontend.bundle_sha256, so
	// its own intermediate manifest.Load re-validation never depends on
	// anything the wasm step below is about to add. Running wasm's
	// checksum patch first would instead re-validate a manifest that
	// still has bundle_sha256 unset if frontend.bundle is true, failing
	// spuriously on a field this step hasn't reached yet.
	if !opts.SkipFrontend {
		if _, err := BuildFrontend(ctx, dir, opts.Debug); err != nil {
			return nil, err
		}
	}

	if !opts.SkipWasm {
		wasmResult, err := BuildWasm(ctx, dir, opts.Debug)
		if err != nil {
			return nil, err
		}
		if wasmResult != nil {
			if err := patchManifestField(manifestPath, "checksum", wasmResult.WasmSHA256); err != nil {
				return nil, err
			}
		}
	}

	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	outputPath := opts.Output
	if outputPath == "" {
		outputPath = filepath.Join(dir, "build", fmt.Sprintf("%s-%s.erp", name, version))
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	if err := writeArchive(outputPath, dir, manifestBytes); err != nil {
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
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", "", fmt.Errorf("read manifest: %w", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", "", fmt.Errorf("parse manifest: %w", err)
	}

	name, _ = decoded["name"].(string)
	version, _ = decoded["version"].(string)
	if name == "" || version == "" {
		return "", "", fmt.Errorf("manifest is missing name/version")
	}

	return name, version, nil
}

func patchManifestField(manifestPath, field, value string) error {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	decoded[field] = value

	patched, err := encodeManifest(decoded)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if _, err := manifest.Load(patched); err != nil {
		return fmt.Errorf("build produced an invalid manifest: %w", err)
	}

	return writeFile(manifestPath, patched)
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
