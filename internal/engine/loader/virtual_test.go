package loader

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

// compileFixture is compileRealFixture generalized to any testdata
// subdirectory — goerp#345's Virtual-model fixtures live alongside
// testdata/realfixture and share its build convention.
func compileFixture(t *testing.T, dir string) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), dir+".wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/"+dir)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/%s: %v\n%s", dir, err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

func TestLoadModule_VirtualModel_ConnectorType_Succeeds(t *testing.T) {
	wasmBytes := compileFixture(t, "virtualfixture")
	rt := newRealFixtureRuntime(t)

	src := Source{
		Name:          "legacy",
		ManifestBytes: manifestJSONWithFields(t, "legacy", wasmBytes, []string{}, map[string]any{"type": "connector"}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)
	if m.Status == module.StatusFailed {
		t.Fatalf("Status = StatusFailed, FailureReason = %q", m.FailureReason)
	}
	t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })
}

func TestLoadModule_VirtualModel_NonConnectorType_Fails(t *testing.T) {
	wasmBytes := compileFixture(t, "virtualfixture")
	rt := newRealFixtureRuntime(t)

	src := Source{
		Name:          "legacy",
		ManifestBytes: manifestJSONWithFields(t, "legacy", wasmBytes, []string{}, map[string]any{"type": "domain"}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)
	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed", m.Status)
	}
	if m.FailureReason == "" {
		t.Error("expected a non-empty FailureReason")
	}
}

func TestLoadModule_VirtualModel_EnableOpsCreateWithoutRegisteredCreate_Fails(t *testing.T) {
	wasmBytes := compileFixture(t, "virtualfixture_createviolation")
	rt := newRealFixtureRuntime(t)

	src := Source{
		Name:          "legacy",
		ManifestBytes: manifestJSONWithFields(t, "legacy", wasmBytes, []string{}, map[string]any{"type": "connector"}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)
	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed", m.Status)
	}
}

func TestLoadModule_VirtualModel_EnableOpsListWithCondition_Fails(t *testing.T) {
	wasmBytes := compileFixture(t, "virtualfixture_listcondition")
	rt := newRealFixtureRuntime(t)

	src := Source{
		Name:          "legacy",
		ManifestBytes: manifestJSONWithFields(t, "legacy", wasmBytes, []string{}, map[string]any{"type": "connector"}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)
	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed", m.Status)
	}
}
