package wasm

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/vmihailenco/msgpack/v5"
)

// memoryConfig is an in-memory ConfigResolver and ConfigStore holding raw
// values in the resolver's bare-text form, keyed by "{module}.{key}".
type memoryConfig struct {
	values map[string]string
}

func (m *memoryConfig) Get(_ context.Context, _, key string) (string, bool, bool, error) {
	v, ok := m.values[key]
	return v, false, ok, nil
}

func (m *memoryConfig) Invalidate(_, _ string) {}

func (m *memoryConfig) SetModuleConfig(_ context.Context, _, _, moduleName, key string, value []byte, valueType string, _ bool, _ string) error {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return err
	}
	if s, ok := decoded.(string); ok && valueType != "json" {
		m.values[moduleName+"."+key] = s
		return nil
	}
	m.values[moduleName+"."+key] = string(value)
	return nil
}

func compileConfigCallerFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "configcallerfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/configcallerfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/configcallerfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// configCallerResult mirrors testdata/configcallerfixture's result envelope.
type configCallerResult struct {
	OK           bool     `msgpack:"ok"`
	Error        string   `msgpack:"error,omitempty"`
	Country      string   `msgpack:"country,omitempty"`
	CountrySet   bool     `msgpack:"country_set,omitempty"`
	Interval     int64    `msgpack:"interval,omitempty"`
	Threshold    float64  `msgpack:"threshold,omitempty"`
	Currencies   []string `msgpack:"currencies,omitempty"`
	APIKey       string   `msgpack:"api_key,omitempty"`
	StrangerFail bool     `msgpack:"stranger_fail,omitempty"`
}

// TestConfigCallerFixture_TypedDefinitions_RoundTripThroughRealModule drives a
// real compiled module's sdk/go/config definitions against the real
// host.config: defaults apply while nothing is stored, writes qualify the
// short key with the module name and read back typed, and a key the module's
// config_schema does not declare is rejected.
func TestConfigCallerFixture_TypedDefinitions_RoundTripThroughRealModule(t *testing.T) {
	ctx := t.Context()
	rt, err := New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 8 << 20,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	store := &memoryConfig{values: map[string]string{}}
	rt.SetTenantConfig(store, store)

	compiled, err := rt.wazero.CompileModule(ctx, compileConfigCallerFixture(t))
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	mc := NewModuleContext("req-1", "testmodule", "user-1", "", nil, nil, "tenant-1", "tenant-slug", "trace-1", 0, nil, ModuleSnapshot{
		ConfigSchema: []manifest.ConfigEntry{
			{Key: "default_country_code", Type: "string", Default: "GH"},
			{Key: "reconcile_interval", Type: "duration", Default: "15m"},
			{Key: "dedup_threshold", Type: "float", Default: 0.85},
			{Key: "currencies", Type: "string[]", Default: []string{"GHS"}},
			{Key: "api_key", Type: "string", Required: true, Encrypted: true},
		},
	})

	inst, err := newModuleInstance(ctx, fmt.Sprintf("configcallerfixture-%d", time.Now().UnixNano()), compiled, rt)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	rt.RegisterInstance(inst)
	t.Cleanup(func() { rt.UnregisterInstance(inst) })

	call := func(export string) configCallerResult {
		t.Helper()
		fn := inst.module.ExportedFunction(export)
		if fn == nil {
			t.Fatalf("fixture has no export %s", export)
		}
		results, err := fn.Call(ctx)
		if err != nil {
			t.Fatalf("call %s: %v", export, err)
		}
		raw, ok := inst.module.Memory().Read(uint32(results[0]>>32), uint32(results[0]))
		if !ok {
			t.Fatalf("read %s result: out of bounds", export)
		}
		var out configCallerResult
		if err := msgpack.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal %s result: %v", export, err)
		}
		if !out.OK {
			t.Fatalf("%s failed: %s", export, out.Error)
		}
		return out
	}

	defaults := call("run_read")
	if defaults.Country != "GH" || defaults.CountrySet || time.Duration(defaults.Interval) != 15*time.Minute ||
		defaults.Threshold != 0.85 || len(defaults.Currencies) != 1 || defaults.Currencies[0] != "GHS" || defaults.APIKey != "" {
		t.Errorf("defaults = %+v", defaults)
	}

	if write := call("run_write"); !write.StrangerFail {
		t.Error("Set on an undeclared key succeeded, want the host to reject it")
	}
	if got := store.values["testmodule.default_country_code"]; got != "NG" {
		t.Errorf("stored country = %q, want NG under the module-qualified key", got)
	}

	stored := call("run_read")
	if stored.Country != "NG" || !stored.CountrySet {
		t.Errorf("country = %q set=%v, want NG set", stored.Country, stored.CountrySet)
	}
	if time.Duration(stored.Interval) != 90*time.Second {
		t.Errorf("interval = %s, want 1m30s", time.Duration(stored.Interval))
	}
	if len(stored.Currencies) != 2 || stored.Currencies[0] != "USD" || stored.Currencies[1] != "EUR" {
		t.Errorf("currencies = %v", stored.Currencies)
	}
}
