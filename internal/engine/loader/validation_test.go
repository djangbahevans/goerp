package loader

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/buildinfo"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func closeLoaded(t *testing.T, modules map[string]*module.LoadedModule) {
	t.Helper()
	t.Cleanup(func() {
		for _, m := range modules {
			// Discovery failures close their pools before LoadModule sets LoadedAt.
			if m.Pool != nil && !m.LoadedAt.IsZero() {
				m.Pool.DrainAndClose(context.Background(), 5*time.Second)
			}
		}
	})
}

func TestLoadAllSupportedCrossModuleErrors(t *testing.T) {

	tests := []struct {
		name          string
		first, second map[string]any
		want          string
	}{
		{
			name:   "missing dependency",
			second: map[string]any{"depends_on": []string{"absent"}},
			want:   "depends_on",
		},
		{
			name:   "conflict",
			second: map[string]any{"conflicts_with": []string{"base"}},
			want:   "conflicts_with",
		},
		{
			name:  "reverse conflict",
			first: map[string]any{"conflicts_with": []string{"candidate"}},
			want:  "conflicts_with",
		},
		{
			name:   "ownership collision",
			first:  map[string]any{"schema": map[string]any{"owned_models": []string{"base.item"}}},
			second: map[string]any{"schema": map[string]any{"owned_models": []string{"base.item"}}},
			want:   "schema.owned_models",
		},
		{
			name: "OAuth collision",
			first: map[string]any{
				"type":           "connector",
				"oauth_provider": map[string]any{"name": "shared"},
			},
			second: map[string]any{
				"type":           "connector",
				"oauth_provider": map[string]any{"name": "shared"},
			},
			want: "oauth_provider.name",
		},
		{
			name:   "missing report",
			second: map[string]any{"report_overrides": []manifest.ReportOverride{{Overrides: "base.absent"}}},
			want:   "report_overrides",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := newTestRuntime(t)
			modules := LoadAll(t.Context(), rt, testPoolCfg(), []Source{
				{Name: "base", ManifestBytes: manifestJSONWithFields(t, "base", okModule, []string{}, tc.first), WasmBytes: okModule},
				{Name: "candidate", ManifestBytes: manifestJSONWithFields(t, "candidate", okModule, []string{}, tc.second), WasmBytes: okModule},
			})
			closeLoaded(t, modules)

			failed := modules["candidate"]
			if modules["base"].Status == module.StatusFailed {
				t.Fatalf("earlier module failed: %s", modules["base"].FailureReason)
			}
			if failed.Status != module.StatusFailed || !strings.Contains(failed.FailureReason, tc.want) {
				t.Fatalf("status = %v, reason = %q, want %q", failed.Status, failed.FailureReason, tc.want)
			}
		})
	}
}

func TestLoadAllDeclaredReportOverrideLoads(t *testing.T) {
	modules := LoadAll(t.Context(), newTestRuntime(t), testPoolCfg(), []Source{
		{
			Name:      "base",
			WasmBytes: okModule,
			ManifestBytes: manifestJSONWithFields(t, "base", okModule, []string{}, map[string]any{
				"reports": []manifest.Report{{Name: "invoice", Label: "Invoice", Template: "invoice.html", DataHandler: "invoice", Formats: []string{"html"}}},
			}),
		},
		{
			Name:      "candidate",
			WasmBytes: okModule,
			ManifestBytes: manifestJSONWithFields(t, "candidate", okModule, []string{}, map[string]any{
				"depends_on":       []string{"base"},
				"report_overrides": []manifest.ReportOverride{{Overrides: "base.invoice", Template: "invoice.html"}},
			}),
		},
	})
	closeLoaded(t, modules)

	for _, m := range modules {
		if m.Status == module.StatusFailed {
			t.Fatal(m.FailureReason)
		}
	}
}

func TestLoadAllPolicyFieldResolution(t *testing.T) {
	binary := compileRealFixture(t)

	tests := []struct {
		name, condition string
		wantFailed      bool
	}{
		{"valid", "record.name = 'A' OR current_user.id = record.created_by", false},
		{"missing field", "record.absent = 'A'", true},
		{"unknown user field", "current_user.absent = record.id", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := newRealFixtureRuntime(t)
			modules := LoadAll(t.Context(), rt, testPoolCfg(), []Source{{Name: "widgets", WasmBytes: binary, ManifestBytes: manifestJSONWithFields(t, "widgets", binary, []string{}, map[string]any{
				"permissions": []manifest.Permission{{Name: "widgets:widget:read"}},
				"policies":    []manifest.Policy{{Name: "widgets:widget:scope", AppliesTo: "widgets:widget:read", Condition: tc.condition}},
			})}})
			closeLoaded(t, modules)

			m := modules["widgets"]
			if (m.Status == module.StatusFailed) != tc.wantFailed {
				t.Fatalf("status=%v, reason=%q", m.Status, m.FailureReason)
			}
		})
	}
}

func TestValidateCandidateRefreshesWarningsWithoutMutatingPublishedModules(t *testing.T) {
	subscriber := &module.LoadedModule{
		Status: module.StatusReady,
		Manifest: manifest.Manifest{
			Name:          "subscriber",
			SoftDependsOn: []string{"emitter"},
			Subscribes:    []manifest.EventSubscription{{Name: "emitter.record.created"}},
		},
	}
	existing := map[string]*module.LoadedModule{"subscriber": subscriber}

	ValidateModuleSet(existing)
	if len(subscriber.Diagnostics) != 1 || subscriber.Diagnostics[0].Rule != manifest.RuleMissingEmitter {
		t.Fatalf("diagnostics=%+v", subscriber.Diagnostics)
	}

	emitter := &module.LoadedModule{
		Status: module.StatusReady,
		Manifest: manifest.Manifest{
			Name:  "emitter",
			Emits: []manifest.EventDeclaration{{Name: "emitter.record.created"}},
		},
	}
	merged, err := ValidateCandidate(emitter, existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged["subscriber"].Diagnostics) != 0 || len(subscriber.Diagnostics) != 1 {
		t.Fatalf("new=%+v, published=%+v", merged["subscriber"].Diagnostics, subscriber.Diagnostics)
	}

	replacement := &module.LoadedModule{Status: module.StatusReady, Manifest: manifest.Manifest{Name: "emitter"}}
	reloaded, err := ValidateCandidate(replacement, merged)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded["subscriber"].Diagnostics) != 1 || len(merged["subscriber"].Diagnostics) != 0 {
		t.Fatalf("reloaded=%+v, previous=%+v", reloaded["subscriber"].Diagnostics, merged["subscriber"].Diagnostics)
	}
}

func TestValidateCandidateRejectsBrokenDependentWithoutChangingSnapshot(t *testing.T) {
	base := &module.LoadedModule{
		Status: module.StatusReady,
		Manifest: manifest.Manifest{
			Name:  "base",
			Emits: []manifest.EventDeclaration{{Name: "base.record.created"}},
		},
	}
	dependent := &module.LoadedModule{
		Status: module.StatusReady,
		Manifest: manifest.Manifest{
			Name:       "dependent",
			DependsOn:  []string{"base"},
			Subscribes: []manifest.EventSubscription{{Name: "base.record.created"}},
		},
	}
	existing := map[string]*module.LoadedModule{"base": base, "dependent": dependent}
	replacement := &module.LoadedModule{Status: module.StatusReady, Manifest: manifest.Manifest{Name: "base"}}

	if _, err := ValidateCandidate(replacement, existing); err == nil || !strings.Contains(err.Error(), "dependent") {
		t.Fatalf("error=%v", err)
	}
	if dependent.Status != module.StatusReady || base.Status != module.StatusReady {
		t.Fatal("published module was changed")
	}
}

func TestComputedPathResolution(t *testing.T) {
	customer := *model.Define("sales.customer").Field("credit", model.Float())
	order := *model.Define("sales.order").Field("name", model.Char()).Field("customer_id", model.Many2One("sales.customer")).Field("lines", model.One2Many("sales.line", "order_id"))
	line := *model.Define("sales.line").Field("quantity", model.Float())
	models := map[string]model.ModelDeclaration{"sales.customer": customer, "sales.order": order, "sales.line": line}
	for _, path := range []string{"name", "customer.credit", "lines.quantity"} {
		if err := checkComputedPath(order, path, models); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	for _, path := range []string{"absent", "customer.absent", "absent.credit", "customer.credit.value", "customer."} {
		if err := checkComputedPath(order, path, models); err == nil {
			t.Fatalf("accepted invalid path %q", path)
		}
	}
}

func TestLoadAllRetainsLocalAndCrossModuleWarnings(t *testing.T) {
	rt := newTestRuntime(t)
	modules := LoadAll(t.Context(), rt, testPoolCfg(), []Source{{Name: "connector", WasmBytes: okModule, ManifestBytes: manifestJSONWithFields(t, "connector", okModule, []string{"http.fetch"}, map[string]any{
		"type":             "connector",
		"http_allowlist":   []string{"http://example.test/*"},
		"long_description": strings.Repeat("x", 4097),
		"config_schema":    []manifest.ConfigEntry{{Key: "required", Type: "string", Required: true}, {Key: "optional", Type: "boolean"}},
		"permissions":      []manifest.Permission{{Name: "connector:record:read", DefaultRoles: []string{"custom"}}},
		"soft_depends_on":  []string{"absent"},
		"subscribes":       []manifest.EventSubscription{{Name: "absent.record.created"}},
	})}})
	closeLoaded(t, modules)

	m := modules["connector"]
	if m.Status == module.StatusFailed {
		t.Fatal(m.FailureReason)
	}
	if len(m.Diagnostics) != 6 {
		t.Fatalf("diagnostics=%+v", m.Diagnostics)
	}
	for _, d := range m.Diagnostics {
		if d.Module != "connector" || d.Severity != "warning" || d.Rule == "" {
			t.Fatalf("incomplete diagnostic=%+v", d)
		}
	}
}

func TestLoadModuleDeclaredMigrationRequiresExport(t *testing.T) {
	binary := []byte(strings.Replace(string(okModule), "get_data_migrations", "not_data_migrations", 1))
	m := LoadModule(t.Context(), newTestRuntime(t), testPoolCfg(), Source{
		Name:      "candidate",
		WasmBytes: binary,
		ManifestBytes: manifestJSONWithFields(t, "candidate", binary, []string{}, map[string]any{
			"schema": map[string]any{"owned_models": []string{}, "has_data_migrations": true},
		}),
	})
	closeLoaded(t, map[string]*module.LoadedModule{"candidate": m})

	if m.Status != module.StatusFailed || !strings.Contains(m.FailureReason, "get_data_migrations") {
		t.Fatalf("status=%v reason=%q", m.Status, m.FailureReason)
	}
}

func TestLoadModuleRejectsIncompatibleEngineRelease(t *testing.T) {
	original := buildinfo.Version
	buildinfo.Version = "0.5.0"
	t.Cleanup(func() { buildinfo.Version = original })
	m := LoadModule(t.Context(), newTestRuntime(t), testPoolCfg(), Source{
		Name: "candidate", WasmBytes: okModule,
		ManifestBytes: manifestJSONWithFields(t, "candidate", okModule, []string{}, map[string]any{"engine": ">=1.0.0"}),
	})
	if m.Status != module.StatusFailed || !strings.Contains(m.FailureReason, "excludes running engine version") {
		t.Fatalf("status=%v reason=%q", m.Status, m.FailureReason)
	}
}

func TestLoadAllComputedDependencyResolution(t *testing.T) {
	for _, tc := range []struct {
		path       string
		wantFailed bool
	}{
		{"name", false},
		{"parent.name", false},
		{"absent", true},
		{"parent.absent", true},
		{"parent.name.value", true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			binary := compileValidationFixture(t, "-X main.dependency="+tc.path)

			modules := LoadAll(t.Context(), newRealFixtureRuntime(t), testPoolCfg(), []Source{
				{
					Name:          "widgets",
					WasmBytes:     binary,
					ManifestBytes: manifestJSON(t, "widgets", binary, []string{}),
				},
			})
			closeLoaded(t, modules)

			m := modules["widgets"]
			if (m.Status == module.StatusFailed) != tc.wantFailed {
				t.Fatalf("status=%v reason=%q", m.Status, m.FailureReason)
			}
			if tc.wantFailed && !strings.Contains(m.FailureReason, "computed dependency") {
				t.Fatalf("unexpected failure: %s", m.FailureReason)
			}
		})
	}
}

func TestLoadAllExpandedReservedNamespaces(t *testing.T) {
	for _, name := range []string{"auth", "admin", "storage", "modules", "connectors"} {
		t.Run(name, func(t *testing.T) {
			modules := LoadAll(t.Context(), newTestRuntime(t), testPoolCfg(), []Source{
				{
					Name:          name,
					WasmBytes:     oneRouteModule,
					ManifestBytes: manifestJSON(t, name, oneRouteModule, []string{}),
				},
			})
			closeLoaded(t, modules)

			m := modules[name]
			if m.Status != module.StatusFailed || !strings.Contains(m.FailureReason, "reserved") {
				t.Fatalf("status=%v reason=%q", m.Status, m.FailureReason)
			}
		})
	}
}

func TestLoadAllExtensionTargetMustBeOwnedByDeclaredModule(t *testing.T) {
	modules := LoadAll(t.Context(), newTestRuntime(t), testPoolCfg(), []Source{
		{Name: "base", WasmBytes: okModule, ManifestBytes: manifestJSONWithFields(t, "base", okModule, []string{}, map[string]any{"schema": map[string]any{"owned_models": []string{"base.item"}}})},
		{Name: "extension", WasmBytes: okModule, ManifestBytes: manifestJSONWithFields(t, "extension", okModule, []string{}, map[string]any{
			"type": "field_extension", "depends_on": []string{"base"},
			"schema":                     map[string]any{"owned_models": []string{}, "extends_module": "base", "extends_models": []string{"base.absent"}},
			"view_extension_definitions": []map[string]any{fieldsExtensionDef("extra", "main")},
		})},
	})
	closeLoaded(t, modules)

	m := modules["extension"]
	if m.Status != module.StatusFailed || !strings.Contains(m.FailureReason, "must be owned") {
		t.Fatalf("status=%v reason=%q", m.Status, m.FailureReason)
	}
	if modules["base"].Status == module.StatusFailed {
		t.Fatal("invalid extension failed its target module")
	}
}

func compileValidationFixture(t *testing.T, flags string) []byte {
	t.Helper()

	output := filepath.Join(t.TempDir(), "validation.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-ldflags", flags, "-o", output, "./testdata/computedfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build validation fixture: %v\n%s", err, out)
	}

	binary, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}

	return binary
}

func TestLoadAllDiscoveredDeclarationErrors(t *testing.T) {
	for _, tc := range []struct{ name, flags, want string }{
		{"sequence format", "-X main.sequenceFormat=W/{year}", "sequence format"},
		{"duplicate routes", "-X main.routeMode=duplicate", "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := compileValidationFixture(t, tc.flags)
			modules := LoadAll(t.Context(), newRealFixtureRuntime(t), testPoolCfg(), []Source{
				{
					Name:          "widgets",
					WasmBytes:     binary,
					ManifestBytes: manifestJSON(t, "widgets", binary, []string{}),
				},
			})
			closeLoaded(t, modules)

			m := modules["widgets"]
			if m.Status != module.StatusFailed || !strings.Contains(m.FailureReason, tc.want) {
				t.Fatalf("status=%v reason=%q", m.Status, m.FailureReason)
			}
		})
	}
}
