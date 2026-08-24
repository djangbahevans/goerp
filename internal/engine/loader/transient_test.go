package loader

import (
	"context"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

func TestLoadModule_TransientModel_Succeeds(t *testing.T) {
	wasmBytes := compileFixture(t, "transientfixture")
	rt := newRealFixtureRuntime(t)

	src := Source{
		Name:          "wizard",
		ManifestBytes: manifestJSON(t, "wizard", wasmBytes, []string{}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)
	if m.Status == module.StatusFailed {
		t.Fatalf("Status = StatusFailed, FailureReason = %q", m.FailureReason)
	}
	t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })
}

func TestLoadModule_TransientModel_EnableOpsList_Fails(t *testing.T) {
	wasmBytes := compileFixture(t, "transientfixture_listviolation")
	rt := newRealFixtureRuntime(t)

	src := Source{
		Name:          "wizard",
		ManifestBytes: manifestJSON(t, "wizard", wasmBytes, []string{}),
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
