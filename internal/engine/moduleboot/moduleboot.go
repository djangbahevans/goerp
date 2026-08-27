// Package moduleboot runs Stage 3 of the engine startup sequence
// (engine-internals.md §2): discover module sources, order them by
// manifest depends_on, load each one via loader.LoadModule with
// dependency-cascade failure, and produce the map loader.LoadAll would —
// ready for registry.ModuleRegistry.Update.
//
// GOERP_DEV's directory-watch/hot-reload mode is out of scope; Discover
// only ever runs once, at startup.
package moduleboot

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/rs/zerolog/log"
)

// Discover reads dir for module sources: either subdirectories containing
// manifest.json and module.wasm (goerp module create's loose output
// layout), or *.erp packages (goerp module build's real packaged output —
// a zip archive with the same two files at its root, manifest-spec.md
// §2). An entry missing either file is skipped with a warning rather than
// failing the whole pass. A dir that doesn't exist yet is not an error —
// it returns a nil slice, same as an empty one.
func Discover(dir string) ([]loader.Source, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read module directory %q: %w", dir, err)
	}

	var sources []loader.Source
	for _, entry := range entries {
		name := entry.Name()

		if !entry.IsDir() {
			if !strings.HasSuffix(name, ".erp") {
				continue
			}

			src, err := readPackageSource(filepath.Join(dir, name))
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", name, err)
			}
			if src != nil {
				sources = append(sources, *src)
			}
			continue
		}

		manifestBytes, err := os.ReadFile(filepath.Join(dir, name, "manifest.json"))
		if err != nil {
			if os.IsNotExist(err) {
				log.Warn().Str("module_dir", name).Msg("missing manifest.json, skipping")
				continue
			}
			return nil, fmt.Errorf("read %s/manifest.json: %w", name, err)
		}

		wasmBytes, err := os.ReadFile(filepath.Join(dir, name, "module.wasm"))
		if err != nil {
			if os.IsNotExist(err) {
				log.Warn().Str("module_dir", name).Msg("missing module.wasm, skipping")
				continue
			}
			return nil, fmt.Errorf("read %s/module.wasm: %w", name, err)
		}

		sources = append(sources, loader.Source{
			Name:          name,
			ManifestBytes: manifestBytes,
			WasmBytes:     wasmBytes,
			PackagePath:   filepath.Join(dir, name),
		})
	}

	return sources, nil
}

var errZipMemberNotFound = errors.New("member not found")

// readZipMember returns the bytes of the first file in r named name, or
// errZipMemberNotFound if none matches. Only ever called with fixed
// literal names (manifest.json, module.wasm) — never an archive-supplied
// path — so zip-slip-style traversal isn't a concern here.
func readZipMember(r *zip.Reader, name string) ([]byte, error) {
	for _, f := range r.File {
		if f.Name != name {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}
		defer rc.Close()

		data, err := io.ReadAll(rc)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		return data, nil
	}

	return nil, errZipMemberNotFound
}

// readPackageSource extracts manifest.json and module.wasm from the .erp
// zip package at path. It returns (nil, nil), not an error, when either
// member is missing — Discover treats that the same as a loose directory
// missing one of the two files: skip with a warning. Source.Name is the
// module's own declared name (manifest.json's "name" field), not the
// archive's own versioned filename (e.g. demo-0.1.0.erp) — falling back
// to the filename only if the manifest fails to parse, so a corrupt
// manifest still surfaces a nameable LoadModule failure downstream rather
// than an empty identifier.
func readPackageSource(path string) (*loader.Source, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open package: %w", err)
	}
	defer r.Close()

	manifestBytes, err := readZipMember(&r.Reader, "manifest.json")
	if err != nil {
		if errors.Is(err, errZipMemberNotFound) {
			log.Warn().Str("package", filepath.Base(path)).Msg("missing manifest.json, skipping")
			return nil, nil
		}
		return nil, err
	}

	wasmBytes, err := readZipMember(&r.Reader, "module.wasm")
	if err != nil {
		if errors.Is(err, errZipMemberNotFound) {
			log.Warn().Str("package", filepath.Base(path)).Msg("missing module.wasm, skipping")
			return nil, nil
		}
		return nil, err
	}

	name := strings.TrimSuffix(filepath.Base(path), ".erp")
	if mf, err := manifest.Load(manifestBytes); err == nil {
		name = mf.Name
	}

	return &loader.Source{
		Name:          name,
		ManifestBytes: manifestBytes,
		WasmBytes:     wasmBytes,
		PackagePath:   path,
	}, nil
}

// Order sorts sources topologically by manifest depends_on, so a module
// never appears before its own dependencies. An unparseable manifest or a
// depends_on naming a module absent from sources is not an ordering
// error — LoadModule surfaces those later, as that module's own failure.
// A cycle fails outright, naming every module in it.
func Order(sources []loader.Source) ([]loader.Source, error) {
	byName := make(map[string]loader.Source, len(sources))
	dependsOn := make(map[string][]string, len(sources))
	for _, src := range sources {
		byName[src.Name] = src
		if mf, err := manifest.Load(src.ManifestBytes); err == nil {
			dependsOn[src.Name] = mf.DependsOn
		}
	}

	ordered := make([]loader.Source, 0, len(sources))
	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(sources))
	var path []string

	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case done:
			return nil
		case visiting:
			cycleStart := slices.Index(path, name)
			cycle := append(append([]string{}, path[cycleStart:]...), name)
			return fmt.Errorf("dependency cycle: %s", strings.Join(cycle, " -> "))
		}

		state[name] = visiting
		path = append(path, name)
		for _, dep := range dependsOn[name] {
			if _, present := byName[dep]; !present {
				continue
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[name] = done
		ordered = append(ordered, byName[name])
		return nil
	}

	for _, src := range sources {
		if err := visit(src.Name); err != nil {
			return nil, err
		}
	}

	return ordered, nil
}

// LoadCascading loads sources — already ordered by Order — via
// loader.LoadModule, matching loader.LoadAll's route-registration and
// event-subscription validation, except: before loading a source, it
// skips it (via LoadedModule.FailDependency) if any of its depends_on is
// already StatusFailed, cascading through transitive dependents too.
func LoadCascading(ctx context.Context, rt *wasm.Runtime, poolCfg wasm.PoolConfig, sources []loader.Source) map[string]*module.LoadedModule {
	modules := make(map[string]*module.LoadedModule, len(sources))
	table := route.New()

	for _, src := range sources {
		mf, err := manifest.Load(src.ManifestBytes)
		if err == nil {
			if upstream, blocked := failedDependency(mf.DependsOn, modules); blocked {
				m := &module.LoadedModule{Manifest: *mf, PackagePath: src.PackagePath}
				m.FailDependency(upstream)
				modules[src.Name] = m
				continue
			}
		}

		m := loader.LoadModule(ctx, rt, poolCfg, src)
		if m.Status != module.StatusFailed {
			explicit := route.ExplicitRoutesFrom(m.ExplicitRoutes)
			if err := route.RegisterModuleRoutes(table, src.Name, m.Manifest.Type, explicit); err != nil {
				m.Fail(err.Error())
			}
		}
		modules[src.Name] = m
	}

	loader.ValidateEventSubscriptions(modules)

	return modules
}

func failedDependency(dependsOn []string, modules map[string]*module.LoadedModule) (string, bool) {
	for _, dep := range dependsOn {
		if m, ok := modules[dep]; ok && m.Status == module.StatusFailed {
			return dep, true
		}
	}
	return "", false
}
