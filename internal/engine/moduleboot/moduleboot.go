// Package moduleboot runs Stage 3 of the engine startup sequence
// (engine-internals.md §2): discover module sources, order them by
// manifest depends_on, load each one via loader.LoadModule with
// dependency-cascade failure, and produce the map loader.LoadAll would —
// ready for registry.ModuleRegistry.Update.
//
// Discover itself only ever runs once, at startup — DiscoverOne is the
// single-path entry point internal/engine/hotreload reuses at runtime,
// once per trigger, instead of rescanning the whole directory.
package moduleboot

import (
	"archive/zip"
	"bytes"
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
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
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
		if !entry.IsDir() && !strings.HasSuffix(name, ".erp") {
			continue
		}

		src, err := DiscoverOne(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if src != nil {
			sources = append(sources, *src)
		}
	}

	return sources, nil
}

// DiscoverOne reads a single module source at path — either a *.erp
// package file or a loose module directory containing manifest.json and
// module.wasm (the same two layouts Discover accepts for a directory
// scan). It returns (nil, nil), not an error, when path's module.wasm or
// manifest.json is missing, mirroring Discover's own "skip one bad entry
// with a warning" behavior. This is what lets a hot-reload trigger —
// which points at exactly one path, not the whole module directory —
// reuse Discover's own parsing logic instead of duplicating it.
func DiscoverOne(path string) (*loader.Source, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", path, err)
	}

	if !info.IsDir() {
		if !strings.HasSuffix(path, ".erp") {
			return nil, fmt.Errorf("%q is not a .erp package or a module directory", path)
		}
		return readPackageSource(path)
	}

	name := filepath.Base(path)

	manifestBytes, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		if os.IsNotExist(err) {
			log.Warn().Str("module_dir", name).Msg("missing manifest.json, skipping")
			return nil, nil
		}
		return nil, fmt.Errorf("read %s/manifest.json: %w", name, err)
	}

	wasmBytes, err := os.ReadFile(filepath.Join(path, "module.wasm"))
	if err != nil {
		if os.IsNotExist(err) {
			log.Warn().Str("module_dir", name).Msg("missing module.wasm, skipping")
			return nil, nil
		}
		return nil, fmt.Errorf("read %s/module.wasm: %w", name, err)
	}

	return &loader.Source{
		Name:          name,
		ManifestBytes: manifestBytes,
		WasmBytes:     wasmBytes,
		PackagePath:   path,
	}, nil
}

var errZipMemberNotFound = errors.New("member not found")

// readZipMember returns the bytes of the first file in r named name, or
// errZipMemberNotFound if none matches. Only ever called with fixed
// literal names (manifest.json, module.wasm) — never an archive-supplied
// path — so zip-slip-style traversal isn't a concern here.
// maxZipMemberSize bounds how much of a single zip member's decompressed
// content readZipMember will hold in memory — generous enough for any
// real manifest.json (manifest.Load's own separate 1MB cap is far
// smaller) or module.wasm, but a hard ceiling on decompression
// amplification: readZipMember is reachable from ParsePackage against an
// untrusted, admin-submitted request body (POST /admin/modules/install),
// where the raw upload is capped by GOERP_ADMIN_MAX_BODY_BYTES but a
// small, highly-compressible entry could otherwise still expand to
// gigabytes during io.ReadAll, before any other validation runs.
const maxZipMemberSize = 128 << 20 // 128 MiB

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

		data, err := io.ReadAll(io.LimitReader(rc, maxZipMemberSize+1))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if len(data) > maxZipMemberSize {
			return nil, fmt.Errorf("%s exceeds the %d byte decompressed size limit", name, maxZipMemberSize)
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

// ParsePackage extracts manifest.json and module.wasm from an in-memory
// .erp package (the same zip layout readPackageSource reads from disk),
// for a caller that receives package bytes directly — e.g. goerp#468's
// POST /admin/modules/install request body — rather than a file already
// on disk. Unlike readPackageSource/Discover, which skip a malformed
// package with a warning (safe for a directory scan that shouldn't abort
// on one bad entry), a missing manifest.json, missing module.wasm, or
// unparseable manifest here is a hard error: installing one
// deliberately-submitted package has no "skip and keep scanning"
// fallback to defer to. The returned Source's PackagePath is empty — the
// caller sets it once the bytes are persisted somewhere on disk (e.g. so
// notiftemplate.Load has a real path to read from). The parsed
// *manifest.Manifest is returned alongside Source so a caller that needs
// a field beyond what Source itself carries (e.g. Version, for naming
// the persisted file) doesn't have to parse ManifestBytes a second time —
// it's already been parsed once to validate the package in the first
// place.
func ParsePackage(data []byte) (*loader.Source, *manifest.Manifest, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, fmt.Errorf("open package: %w", err)
	}

	manifestBytes, err := readZipMember(r, "manifest.json")
	if err != nil {
		if errors.Is(err, errZipMemberNotFound) {
			return nil, nil, fmt.Errorf("package has no manifest.json")
		}
		return nil, nil, err
	}

	wasmBytes, err := readZipMember(r, "module.wasm")
	if err != nil {
		if errors.Is(err, errZipMemberNotFound) {
			return nil, nil, fmt.Errorf("package has no module.wasm")
		}
		return nil, nil, err
	}

	mf, err := manifest.Load(manifestBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid manifest: %w", err)
	}

	return &loader.Source{
		Name:          mf.Name,
		ManifestBytes: manifestBytes,
		WasmBytes:     wasmBytes,
	}, mf, nil
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
			if suppressed, err := route.RegisterRoutes(table, src.Name, m.Manifest.Type, explicit, m.ModelDecls); err != nil {
				m.Fail(err.Error())
			} else {
				for _, s := range suppressed {
					log.Warn().Str("module", src.Name).Str("model", s.Model).Str("op", s.Op).
						Msg("EnableOps: explicit route already registered, auto-derived route suppressed")
				}
			}
		}
		if m.Status != module.StatusFailed && len(m.Manifest.NotificationTypes) > 0 {
			nt, err := notiftemplate.Load(m.Manifest.NotificationTypes, m.PackagePath)
			if err != nil {
				m.Fail(err.Error())
			} else {
				m.NotifTemplates = nt
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
