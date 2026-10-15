package module

import (
	"encoding/json/v2"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeDevSource(t *testing.T, dir, name string, dependencies ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeDevManifest(t, dir, map[string]any{"name": name, "depends_on": dependencies})
	return dir
}

func writeDevManifest(t *testing.T, dir string, fields map[string]any) {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func devSourceNames(sources []devSource) []string {
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.manifest.Name)
	}
	return names
}

func TestDevSourcesSelectDependencyClosure(t *testing.T) {
	repo := t.TempDir()
	parent := filepath.Join(repo, "modules")
	current := writeDevSource(t, filepath.Join(parent, "sales-source"), "sales", "orders", "contacts")
	writeDevSource(t, filepath.Join(parent, "orders-source"), "orders", "contacts")
	writeDevSource(t, filepath.Join(parent, "contacts-source"), "contacts")
	writeDevSource(t, filepath.Join(parent, "unused-source"), "unused", "missing")
	writeDevSource(t, filepath.Join(parent, "optional-source"), "optional", "contacts")
	writeDevManifest(t, current, map[string]any{
		"name":            "sales",
		"depends_on":      []string{"orders", "contacts"},
		"soft_depends_on": []string{"optional", "absent"},
	})

	for _, tc := range []struct {
		name     string
		includes []string
		want     []string
	}{
		{name: "hard dependencies", want: []string{"contacts", "orders", "sales"}},
		{name: "explicit optional", includes: []string{"optional", "optional", "orders"}, want: []string{"contacts", "orders", "sales", "optional"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sources, err := resolveDevSources(current, repo, []string{"..", parent, parent}, tc.includes)
			if err != nil {
				t.Fatal(err)
			}
			if got := devSourceNames(sources); !slices.Equal(got, tc.want) {
				t.Fatalf("selected %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDevSourcesAdditionalParentsAndSymlinks(t *testing.T) {
	repo := t.TempDir()
	current := writeDevSource(t, filepath.Join(repo, "external", "sales"), "sales", "contacts")
	dependency := writeDevSource(t, filepath.Join(repo, "shared", "source-name"), "contacts")
	alias := filepath.Join(repo, "shared", "alias")
	if err := os.Symlink(dependency, alias); err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{".hidden", "node_modules", "build", "dist", "vendor", "testdata"} {
		writeDevSource(t, filepath.Join(repo, "shared", excluded), "contacts")
	}
	writeDevSource(t, filepath.Join(repo, "shared", "nested", "too-deep"), "contacts")

	sources, err := resolveDevSources(current, repo, []string{"../../shared"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := devSourceNames(sources); !slices.Equal(got, []string{"contacts", "sales"}) || sources[0].dir != dependency {
		t.Fatalf("unexpected discovered set: %+v", sources)
	}
}

func TestDevSourceDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, dependency, include, want string
	}{
		{name: "missing dependency", dependency: "absent", want: "sales -> contacts -> absent"},
		{name: "cycle", dependency: "sales", want: "sales -> contacts -> sales"},
		{name: "missing inclusion", include: "absent", want: "missing module \"absent\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			parent := filepath.Join(repo, "modules")
			current := writeDevSource(t, filepath.Join(parent, "sales"), "sales", "contacts")
			var deps []string
			if tc.dependency != "" {
				deps = append(deps, tc.dependency)
			}
			writeDevSource(t, filepath.Join(parent, "contacts"), "contacts", deps...)
			var includes []string
			if tc.include != "" {
				includes = append(includes, tc.include)
			}

			_, err := resolveDevSources(current, repo, nil, includes)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}

	t.Run("ambiguity lists sources", func(t *testing.T) {
		repo := t.TempDir()
		current := writeDevSource(t, filepath.Join(repo, "modules", "one"), "sales")
		other := writeDevSource(t, filepath.Join(repo, "modules", "two"), "sales")
		_, err := resolveDevSources(current, repo, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), current) || !strings.Contains(err.Error(), other) {
			t.Fatalf("ambiguity must list both source paths: %v", err)
		}
	})

	t.Run("invalid discovery inputs", func(t *testing.T) {
		repo := t.TempDir()
		current := writeDevSource(t, filepath.Join(repo, "current"), "sales")
		if _, err := resolveDevSources(current, repo, []string{"missing"}, nil); err == nil || !strings.Contains(err.Error(), "read modules directory") {
			t.Fatalf("invalid explicit parent accepted: %v", err)
		}
		writeDevSource(t, filepath.Join(repo, "modules", "invalid"), "../unsafe")
		if _, err := resolveDevSources(current, repo, nil, nil); err == nil || !strings.Contains(err.Error(), "requires a module name") {
			t.Fatalf("invalid source identity accepted: %v", err)
		}
	})
}

func TestDevSourceTopologyAndInfrastructure(t *testing.T) {
	dir := writeDevSource(t, t.TempDir(), "dependency")
	baseline := map[string]any{
		"name":                  "dependency",
		"depends_on":            []string{},
		"soft_depends_on":       []string{"optional"},
		"frontend":              map[string]any{"bundle": true},
		"workflow_types":        []any{map[string]any{"name": "job"}},
		"analytics_projections": []any{map[string]any{"name": "report"}},
	}
	writeDevManifest(t, dir, baseline)
	source, err := readDevSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := devSourceOptions(devOptions{}, []devSource{{}, source})
	if !opts.workflows || !opts.analytics || !slices.Contains(devInfraServices(opts), "temporal") || devClickHouseDSN(opts) == "" {
		t.Fatalf("dependency requirements not aggregated: %+v", opts)
	}

	for _, field := range []string{"name", "depends_on", "soft_depends_on", "frontend", "workflow_types", "analytics_projections"} {
		t.Run(field, func(t *testing.T) {
			fields := maps.Clone(baseline)
			delete(fields, field)
			if field == "name" {
				fields[field] = "renamed"
			}
			if field == "depends_on" {
				fields[field] = []string{"added"}
			}
			writeDevManifest(t, dir, fields)
			if err := source.checkTopology(); err == nil || !strings.Contains(err.Error(), "restart goerp module dev") {
				t.Fatalf("topology change must require restart: %v", err)
			}
		})
	}
	baseline["version"] = "1.2.0"
	writeDevManifest(t, dir, baseline)
	if err := source.checkTopology(); err != nil {
		t.Fatalf("ordinary manifest edits require no restart: %v", err)
	}

	args := source.buildArgs("/session/dependency.erp", "/storage", false)
	if slices.Contains(args, "--no-frontend") || !slices.Contains(args, "--worker-storage") {
		t.Fatalf("startup must build complete dependency packages and stage workers: %v", args)
	}
	if !slices.Contains(source.buildArgs("out", "storage", true), "--no-frontend") {
		t.Fatal("backend rebuild unexpectedly compiles the frontend")
	}
}

func TestDevDependencyRebuildRecovery(t *testing.T) {
	for _, failure := range []string{"compile", "reload", "topology", "success"} {
		t.Run(failure, func(t *testing.T) {
			session := t.TempDir()
			modules := filepath.Join(session, "modules")
			if err := os.Mkdir(modules, 0o700); err != nil {
				t.Fatal(err)
			}
			sourceDir := writeDevSource(t, t.TempDir(), "dependency")
			source, err := readDevSource(sourceDir)
			if err != nil {
				t.Fatal(err)
			}
			active := filepath.Join(modules, "dependency.erp")
			for _, path := range []string{active, active + ".sha256"} {
				if err := os.WriteFile(path, []byte("working"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			reloaded := false
			build := func(output string) error {
				for _, path := range []string{output, output + ".sha256"} {
					if err := os.WriteFile(path, []byte("candidate"), 0o600); err != nil {
						return err
					}
				}
				if failure == "compile" {
					return errors.New("compiler failure")
				}
				if failure == "topology" {
					writeDevManifest(t, sourceDir, map[string]any{"name": "dependency", "depends_on": []string{"added"}})
				}
				return nil
			}
			reload := func(data []byte) error {
				reloaded = true
				if string(data) != "candidate" {
					t.Fatalf("unexpected reload payload %q", data)
				}
				if failure == "reload" {
					return errors.New("schema rejected")
				}
				return nil
			}

			err = rebuildDevSource(source, session, modules, build, reload)
			if (err == nil) != (failure == "success") {
				t.Fatalf("unexpected rebuild outcome: %v", err)
			}
			if reloaded != (failure == "success" || failure == "reload") {
				t.Fatal("invalid candidate reached reload")
			}
			want := "working"
			if failure == "success" {
				want = "candidate"
			}
			for _, path := range []string{active, active + ".sha256"} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != want {
					t.Fatalf("active artifact %s = %q, %v, want %q", path, data, err, want)
				}
			}
			entries, err := os.ReadDir(session)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary candidate leaked: %v, %v", entries, err)
			}
		})
	}
}

func TestDevRejectsGraphBeforeToolsOrServices(t *testing.T) {
	repo := t.TempDir()
	for _, path := range []string{"compose.dev.yml", "cmd/engine/main.go", "shell/package.json"} {
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	current := writeDevSource(t, filepath.Join(repo, "modules", "sales"), "sales", "missing")
	t.Chdir(current)
	t.Setenv("PATH", "")
	err := runDev(t.Context(), io.Discard, io.Discard, devOptions{port: 8080, adminPort: 8081, uiPort: 5173})
	if err == nil || !strings.Contains(err.Error(), "sales -> missing") {
		t.Fatalf("invalid graph must fail before tool lookup or infrastructure commands: %v", err)
	}
}

func TestDevChecksEverySelectedModuleLoaded(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{name: "ready", body: `{"modules":{"ready":3},"failed_modules":[]}`},
		{name: "included module failed", body: `{"modules":{"ready":2},"failed_modules":[{"name":"optional","reason":"invalid declaration"}]}`, want: "optional failed to load: invalid declaration"},
		{name: "missing module", body: `{"modules":{"ready":2},"failed_modules":[]}`, want: "2 ready, expected 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			err := checkDevModules(t.Context(), server.URL, 3)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("readiness error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDevSingleSourceAndRepeatedFlags(t *testing.T) {
	repo := t.TempDir()
	current := writeDevSource(t, filepath.Join(repo, "standalone"), "standalone")
	sources, err := resolveDevSources(current, repo, nil, nil)
	if err != nil || !slices.Equal(devSourceNames(sources), []string{"standalone"}) {
		t.Fatalf("standalone module selection: %v, %v", sources, err)
	}
	if opts := devSourceOptions(devOptions{}, sources); opts.workflows || opts.analytics {
		t.Fatalf("standalone module starts unnecessary infrastructure: %+v", opts)
	}

	cmd := newDevCmd()
	if err := cmd.ParseFlags([]string{"--modules-dir", "first", "--modules-dir", "second", "--include", "one", "--include", "two"}); err != nil {
		t.Fatal(err)
	}
	for flag, want := range map[string][]string{
		"modules-dir": {"first", "second"},
		"include":     {"one", "two"},
	} {
		got, err := cmd.Flags().GetStringArray(flag)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("repeated --%s = %v, %v, want %v", flag, got, err, want)
		}
	}
}
