package loader

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

func loadWebhookFixture(t *testing.T, moduleType string) *module.LoadedModule {
	t.Helper()

	wasmBytes := compileFixture(t, "webhookfixture")
	rt := newRealFixtureRuntime(t)
	src := Source{
		Name:          "hooks",
		ManifestBytes: manifestJSONWithFields(t, "hooks", wasmBytes, []string{}, map[string]any{"type": moduleType}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(t.Context(), rt, testPoolCfg(), src)
	if m.Status != module.StatusFailed {
		t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })
	}
	return m
}

func TestLoadModule_WebhookVerifier_ConnectorType_Succeeds(t *testing.T) {
	m := loadWebhookFixture(t, "connector")

	if m.Status == module.StatusFailed {
		t.Fatalf("Status = StatusFailed, FailureReason = %q", m.FailureReason)
	}
	if !m.HasWebhookVerifier {
		t.Error("HasWebhookVerifier = false for a connector exporting handle_webhook_verify")
	}
}

func TestLoadModule_WebhookVerifier_NonConnectorType_Fails(t *testing.T) {
	m := loadWebhookFixture(t, "domain")

	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed", m.Status)
	}
	if !strings.Contains(m.FailureReason, "handle_webhook_verify") {
		t.Errorf("FailureReason = %q, want it to name handle_webhook_verify", m.FailureReason)
	}
}

func TestLoadModule_WithoutWebhookVerifier_HasNone(t *testing.T) {
	wasmBytes := compileFixture(t, "virtualfixture")
	rt := newRealFixtureRuntime(t)
	src := Source{
		Name:          "legacy",
		ManifestBytes: manifestJSONWithFields(t, "legacy", wasmBytes, []string{}, map[string]any{"type": "connector"}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(t.Context(), rt, testPoolCfg(), src)
	if m.Status == module.StatusFailed {
		t.Fatalf("Status = StatusFailed, FailureReason = %q", m.FailureReason)
	}
	t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })
	if m.HasWebhookVerifier {
		t.Error("HasWebhookVerifier = true for a module with no handle_webhook_verify export")
	}
}

func TestLoadModule_TwoWebhookVerifiers_Fails(t *testing.T) {
	wasmBytes := compileFixture(t, "webhookfixture_duplicate")
	rt := newRealFixtureRuntime(t)
	src := Source{
		Name:          "hooks",
		ManifestBytes: manifestJSONWithFields(t, "hooks", wasmBytes, []string{}, map[string]any{"type": "connector"}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(t.Context(), rt, testPoolCfg(), src)
	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed for a module registering two verifiers", m.Status)
	}
	if !strings.Contains(m.FailureReason, "gopanic") || !strings.Contains(m.FailureReason, "main.init") {
		t.Errorf("FailureReason = %q, want an init() panic", m.FailureReason)
	}
}
