package module

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const scaffoldGoVersion = "1.27.0"

func Create(dir, name, moduleType, org string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create module directory: %w", err)
	}

	modulePath := name
	if org != "" {
		modulePath = strings.TrimSuffix(org, "/") + "/" + name
	}
	if err := writeFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo %s\n", modulePath, scaffoldGoVersion)); err != nil {
		return err
	}

	manifestJSON, err := encodeManifest(manifestTemplate(name, moduleType))
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := writeFile(filepath.Join(dir, "manifest.json"), manifestJSON); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(dir, "cmd", "module"), 0o755); err != nil {
		return fmt.Errorf("create cmd/module directory: %w", err)
	}
	if err := writeFile(filepath.Join(dir, "cmd", "module", "main.go"), []byte("package main\n\nfunc main() {}\n")); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
		return fmt.Errorf("create internal directory: %w", err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "translations"), 0o755); err != nil {
		return fmt.Errorf("create translations directory: %w", err)
	}
	if err := writeFile(filepath.Join(dir, "translations", "en.json"), []byte("{}\n")); err != nil {
		return err
	}

	return nil
}

func manifestTemplate(name, moduleType string) map[string]any {
	return map[string]any{
		"name":         name,
		"display_name": displayName((name)),
		"type":         moduleType,
		"version":      "0.1.0",
		"description":  fmt.Sprintf("%s module", name),
		"abi_version":  "1",
		"engine":       ">=0.1.0",
		"depends_on":   []string{},
		"capabilities": []string{},
		"schema": map[string]any{
			"owned_models": []string{},
		},
		"checksum": "sha256:empty",
	}
}

func displayName(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}

	return strings.Join(parts, " ")
}

func writeFile(path string, content []byte) error {
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

// encodeManifest matches v1's json.Encoder: two-space indent, a trailing
// newline, and (Deterministic) sorted map keys — v2 randomizes map key
// order per call by default.
func encodeManifest(v any) ([]byte, error) {
	out, err := json.Marshal(v, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return nil, err
	}

	return append(out, '\n'), nil
}
