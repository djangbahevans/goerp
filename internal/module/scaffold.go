package module

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/semver"
)

const scaffoldGoVersion = "1.27.0"

// scaffoldSchemaGo is the module's own model.Schema declaration — an
// importable package (go-sdk-reference.md §22) so goerp module
// generate can build and run it in isolation, without pulling in the
// rest of the module.
const scaffoldSchemaGo = `package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{}
`

// scaffoldMainGo is a %s-templated string — modulePath fills the schema
// package's import path. It declares every export the engine's loader
// and instance pool invoke that the SDK has a dispatcher for.
const scaffoldMainGo = `package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"%s/schema"
)

//go:wasmexport handle_request
func handleRequest(ptr, length uint32) uint64 {
	return engine.DispatchRequest(ptr, length)
}

//go:wasmexport handle_event
func handleEvent(ptr, length uint32) uint32 {
	return engine.DispatchEvent(ptr, length)
}

//go:wasmexport get_routes
func getRoutes() uint64 {
	return engine.SerialiseRouteTable()
}

//go:wasmexport get_model_declarations
func getModelDeclarations() uint64 {
	return engine.WriteModels(schema.Schema)
}

//go:wasmexport get_data_migrations
func getDataMigrations() uint64 {
	return engine.WriteDataMigrations(nil)
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
`

// SDKModulePath is the Go module the scaffolded code imports the SDK from.
const SDKModulePath = "github.com/djangbahevans/goerp"

// SDKVersion is the SDK version matching the running binary, or "" when it
// has none to pin: a local build is "(devel)" or a "+dirty" pseudo-version.
func SDKVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Path != SDKModulePath {
		return ""
	}
	if v := info.Main.Version; semver.IsValid(v) && semver.Build(v) == "" {
		return v
	}

	return ""
}

// Create scaffolds a module in dir. A non-empty sdkVersion is written as
// the go.mod require for SDKModulePath; go.sum is left to Tidy.
func Create(dir, name, moduleType, org, sdkVersion string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create module directory: %w", err)
	}

	modulePath := name
	if org != "" {
		modulePath = strings.TrimSuffix(org, "/") + "/" + name
	}
	if err := writeFile(filepath.Join(dir, "go.mod"), goModContent(modulePath, sdkVersion)); err != nil {
		return err
	}

	manifestJSON, err := encodeManifest(manifestTemplate(name, moduleType))
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := writeFile(filepath.Join(dir, "manifest.json"), manifestJSON); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(dir, "schema"), 0o755); err != nil {
		return fmt.Errorf("create schema directory: %w", err)
	}
	if err := writeFile(filepath.Join(dir, "schema", "schema.go"), []byte(scaffoldSchemaGo)); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(dir, "cmd", "module"), 0o755); err != nil {
		return fmt.Errorf("create cmd/module directory: %w", err)
	}
	if err := writeFile(filepath.Join(dir, "cmd", "module", "main.go"), fmt.Appendf(nil, scaffoldMainGo, modulePath)); err != nil {
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

// Tidy runs `go mod tidy` in dir, resolving the SDK and writing go.sum.
func Tidy(ctx context.Context, dir string) error {
	return runCmd(ctx, dir, nil, "go", "mod", "tidy")
}

func goModContent(modulePath, sdkVersion string) []byte {
	content := fmt.Appendf(nil, "module %s\n\ngo %s\n", modulePath, scaffoldGoVersion)
	if sdkVersion != "" {
		content = fmt.Appendf(content, "\nrequire %s %s\n", SDKModulePath, sdkVersion)
	}

	return content
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
