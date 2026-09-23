package loader

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

var bundleBytes = []byte("export default function widget() {}")

func bundleSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum)
}

func TestLoadModule_FrontendBundleTrue_ValidBundleSucceeds(t *testing.T) {
	rt := newTestRuntime(t)
	manifestBytes := manifestJSONWithFields(t, "widgets", okModule, []string{"db.read"}, map[string]any{
		"frontend": map[string]any{"bundle": true, "bundle_sha256": bundleSHA256(bundleBytes)},
	})
	src := Source{Name: "widgets", ManifestBytes: manifestBytes, WasmBytes: okModule, BundleBytes: bundleBytes}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusSyncing {
		t.Fatalf("Status = %v, want StatusSyncing; FailureReason = %q", m.Status, m.FailureReason)
	}
}

func TestLoadModule_FrontendBundleTrue_MissingBundleFails(t *testing.T) {
	rt := newTestRuntime(t)
	manifestBytes := manifestJSONWithFields(t, "widgets", okModule, []string{"db.read"}, map[string]any{
		"frontend": map[string]any{"bundle": true, "bundle_sha256": bundleSHA256(bundleBytes)},
	})
	src := Source{Name: "widgets", ManifestBytes: manifestBytes, WasmBytes: okModule}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed", m.Status)
	}
	if !strings.Contains(m.FailureReason, "bundle") {
		t.Errorf("FailureReason = %q, want it to mention the bundle", m.FailureReason)
	}
}

func TestLoadModule_FrontendBundleTrue_ChecksumMismatchFails(t *testing.T) {
	rt := newTestRuntime(t)
	manifestBytes := manifestJSONWithFields(t, "widgets", okModule, []string{"db.read"}, map[string]any{
		"frontend": map[string]any{"bundle": true, "bundle_sha256": bundleSHA256(bundleBytes)},
	})
	corrupted := append([]byte(nil), bundleBytes...)
	corrupted = append(corrupted, '!')
	src := Source{Name: "widgets", ManifestBytes: manifestBytes, WasmBytes: okModule, BundleBytes: corrupted}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed", m.Status)
	}
	if !strings.Contains(m.FailureReason, "checksum") {
		t.Errorf("FailureReason = %q, want it to mention the checksum", m.FailureReason)
	}
}

func TestLoadModule_NoFrontendBundle_BundleBytesIgnored(t *testing.T) {
	rt := newTestRuntime(t)
	// No frontend field at all, but BundleBytes is set anyway — LoadModule
	// never verifies anything it wasn't declared to expect.
	src := Source{
		Name:          "widgets",
		ManifestBytes: manifestJSON(t, "widgets", okModule, []string{"db.read"}),
		WasmBytes:     okModule,
		BundleBytes:   bundleBytes,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusSyncing {
		t.Fatalf("Status = %v, want StatusSyncing; FailureReason = %q", m.Status, m.FailureReason)
	}
}

func TestLoadModule_FrontendBundleFalse_MissingBundleSucceeds(t *testing.T) {
	rt := newTestRuntime(t)
	manifestBytes := manifestJSONWithFields(t, "widgets", okModule, []string{"db.read"}, map[string]any{
		"frontend": map[string]any{"bundle": false},
	})
	src := Source{Name: "widgets", ManifestBytes: manifestBytes, WasmBytes: okModule}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusSyncing {
		t.Fatalf("Status = %v, want StatusSyncing; FailureReason = %q", m.Status, m.FailureReason)
	}
}
