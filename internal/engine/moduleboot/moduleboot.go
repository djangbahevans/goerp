// Package moduleboot runs Stage 3 of the engine startup sequence
// (engine-internals.md §2): discover module sources, order them by
// manifest depends_on, load each one via loader.LoadModule with
// dependency-cascade failure, and produce the map loader.LoadAll would —
// ready for registry.ModuleRegistry.Update.
//
// Packed .erp archives and GOERP_DEV's directory-watch/hot-reload mode are
// out of scope; Discover only ever runs once, at startup.
package moduleboot

import (
	"context"
	"fmt"
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

// Discover reads dir for module subdirectories, each containing
// manifest.json and module.wasm (goerp module build/create's own output
// layout). A subdirectory missing either file is skipped with a warning
// rather than failing the whole pass. A dir that doesn't exist yet is not
// an error — it returns a nil slice, same as an empty one.
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
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()

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

		sources = append(sources, loader.Source{Name: name, ManifestBytes: manifestBytes, WasmBytes: wasmBytes})
	}

	return sources, nil
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
				m := &module.LoadedModule{Manifest: *mf}
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
