package loader

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
)

func TestLoadHookHandlers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", path, "../wasm/testdata/hookfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	wasmtest.Precompile(t, data, wasmtest.SharedCompilationCacheDir())

	noDispatcher := compileRealFixture(t)
	wrongSignature := invalidHookSignatureModule()
	rt := newRealFixtureRuntime(t)

	for _, tc := range []struct {
		name    string
		binary  []byte
		hook    string
		handler string
		want    string
	}{
		{name: "registered", binary: data, hook: abi.HookRequestBefore, handler: "append_a"},
		{name: "validation does not run handler", binary: data, hook: abi.HookRequestBefore, handler: "reject"},
		{name: "missing handler", binary: data, hook: abi.HookRequestBefore, handler: "missing", want: "not registered"},
		{name: "missing dispatcher", binary: noDispatcher, hook: abi.HookRequestBefore, handler: "append_a", want: "handle_hook"},
		{name: "invalid dispatcher signature", binary: wrongSignature, hook: abi.HookRequestBefore, handler: "append_a", want: "handle_hook"},
		{name: "custom declaration only", binary: noDispatcher, hook: "accounting.compute_taxes", handler: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string]any{"hooks": []map[string]any{{"hook": tc.hook, "handler": tc.handler}}}
			src := Source{Name: "hooks", WasmBytes: tc.binary, ManifestBytes: manifestJSONWithFields(t, "hooks", tc.binary, []string{}, fields)}
			loaded := LoadModule(t.Context(), rt, testPoolCfg(), src)
			if tc.want != "" {
				if loaded.Status != module.StatusFailed || !strings.Contains(loaded.FailureReason, tc.want) {
					t.Fatalf("status = %v, reason = %q", loaded.Status, loaded.FailureReason)
				}
				return
			}
			if loaded.Status == module.StatusFailed {
				t.Fatal(loaded.FailureReason)
			}
			t.Cleanup(func() { loaded.Pool.DrainAndClose(context.Background(), 5*time.Second) })
		})
	}
}

func invalidHookSignatureModule() []byte {
	data := slices.Clone(okModule[:8])
	for offset := 8; offset < len(okModule); {
		section := okModule[offset]
		size, length := binary.Uvarint(okModule[offset+1:])
		start := offset + 1 + length
		end := start + int(size)
		if section != 7 {
			data = append(data, okModule[offset:end]...)
		} else {
			payload := slices.Clone(okModule[start:end])
			payload[0]++
			payload = append(payload, 11)
			payload = append(payload, "handle_hook"...)
			payload = append(payload, 0, 2)
			data = append(data, section)
			data = binary.AppendUvarint(data, uint64(len(payload)))
			data = append(data, payload...)
		}
		offset = end
	}

	return data
}
