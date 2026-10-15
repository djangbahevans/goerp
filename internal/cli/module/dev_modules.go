package module

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

type devSource struct {
	dir      string
	manifest manifest.Manifest
}

var devModuleName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func readDevSource(dir string) (devSource, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return devSource{}, fmt.Errorf("read module manifest in %s: %w", dir, err)
	}

	var m manifest.Manifest
	// Source manifests receive worker and frontend checksums during packaging.
	if err := json.Unmarshal(data, &m); err != nil {
		return devSource{}, fmt.Errorf("parse manifest in %s: %w", dir, err)
	}
	if !devModuleName.MatchString(m.Name) {
		return devSource{}, fmt.Errorf("manifest in %s requires a module name starting with a lowercase letter, followed by lowercase letters, digits or underscores (max 64 characters)", dir)
	}

	return devSource{dir: dir, manifest: m}, nil
}

func resolveDevSources(current, repo string, locations, includes []string) ([]devSource, error) {
	index := make(map[string]devSource)
	seen := make(map[string]bool)
	add := func(dir string) error {
		path, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return fmt.Errorf("resolve module source %s: %w", dir, err)
		}
		if seen[path] {
			return nil
		}
		seen[path] = true

		source, err := readDevSource(path)
		if err != nil {
			return err
		}
		if other, ok := index[source.manifest.Name]; ok {
			return fmt.Errorf("ambiguous module %q: %s and %s", source.manifest.Name, other.dir, path)
		}
		index[source.manifest.Name] = source
		return nil
	}

	if err := add(current); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(current)
	if err != nil {
		return nil, err
	}
	currentSource, err := readDevSource(root)
	if err != nil {
		return nil, err
	}

	search := append([]string{filepath.Join(repo, "modules")}, locations...)
	for i, location := range search {
		if !filepath.IsAbs(location) {
			location = filepath.Join(current, location)
		}
		entries, err := os.ReadDir(location)
		if i == 0 && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read modules directory %s: %w", location, err)
		}

		for _, entry := range entries {
			if ignoredDevSourceDir(entry.Name()) {
				continue
			}
			dir := filepath.Join(location, entry.Name())
			info, err := os.Stat(dir)
			if err != nil {
				return nil, fmt.Errorf("inspect module source %s: %w", dir, err)
			}
			if !info.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, "manifest.json")); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return nil, fmt.Errorf("inspect manifest in %s: %w", dir, err)
			}
			if err := add(dir); err != nil {
				return nil, err
			}
		}
	}

	var selected []devSource
	state := make(map[string]int)
	var visit func(string, []string) error
	visit = func(name string, chain []string) error {
		chain = append(slices.Clone(chain), name)
		switch state[name] {
		case 1:
			return fmt.Errorf("module dependency cycle: %s", strings.Join(chain, " -> "))
		case 2:
			return nil
		}
		source, ok := index[name]
		if !ok {
			return fmt.Errorf("missing module %q (dependency chain: %s); add its parent directory with --modules-dir", name, strings.Join(chain, " -> "))
		}
		state[name] = 1
		for _, dependency := range source.manifest.DependsOn {
			if err := visit(dependency, chain); err != nil {
				return err
			}
		}
		state[name] = 2
		selected = append(selected, source)
		return nil
	}

	for _, name := range append([]string{currentSource.manifest.Name}, includes...) {
		if err := visit(name, nil); err != nil {
			return nil, err
		}
	}
	return selected, nil
}

func ignoredDevSourceDir(name string) bool {
	return strings.HasPrefix(name, ".") || slices.Contains([]string{"node_modules", "build", "dist", "vendor", "testdata"}, name)
}

func (s devSource) frontend() bool {
	return s.manifest.Frontend != nil && s.manifest.Frontend.Bundle != nil && *s.manifest.Frontend.Bundle
}

func (s devSource) checkTopology() error {
	next, err := readDevSource(s.dir)
	if err != nil {
		return err
	}
	if next.manifest.Name != s.manifest.Name ||
		!slices.Equal(next.manifest.DependsOn, s.manifest.DependsOn) ||
		!slices.Equal(next.manifest.SoftDependsOn, s.manifest.SoftDependsOn) ||
		next.frontend() != s.frontend() ||
		(len(next.manifest.WorkflowTypes) > 0) != (len(s.manifest.WorkflowTypes) > 0) ||
		(len(next.manifest.AnalyticsProjections) > 0) != (len(s.manifest.AnalyticsProjections) > 0) {
		return fmt.Errorf("module %s changes the session topology; restart goerp module dev to apply name, dependency, frontend.bundle, workflow or analytics requirement changes", s.manifest.Name)
	}
	return nil
}

func devSourceOptions(opts devOptions, sources []devSource) devOptions {
	for _, source := range sources {
		opts.workflows = opts.workflows || len(source.manifest.WorkflowTypes) > 0
		opts.analytics = opts.analytics || len(source.manifest.AnalyticsProjections) > 0
	}
	return opts
}

func (s devSource) buildArgs(output, storage string, watch bool) []string {
	args := []string{"module", "build", s.dir, "--output", output, "--worker-storage", storage}
	if !s.frontend() || watch {
		args = append(args, "--no-frontend")
	}
	return args
}

func (s *devProcesses) buildDevSources(repo, cliPath, modulesDir, storageDir string, sources []devSource) error {
	for _, source := range sources {
		output := filepath.Join(modulesDir, source.manifest.Name+".erp")
		if err := s.run(repo, nil, s.stdout, cliPath, source.buildArgs(output, storageDir, false)...); err != nil {
			return fmt.Errorf("build module %s: %w", source.manifest.Name, err)
		}
	}
	return nil
}
