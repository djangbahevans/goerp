package wasm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/vmihailenco/msgpack/v5"
)

func TestInvokeHandleHookRealSDKAndCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", path, "./testdata/hookfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	wasmtest.Precompile(t, data, wasmtest.SharedCompilationCacheDir())

	rt := newTestRuntime(t, 64<<20)
	compiled, err := rt.CompileModule(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
	inst, err := newModuleInstance(t.Context(), "hooks", compiled, rt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Module().Close(context.Background()) })

	for i, tc := range []struct {
		handler string
		want    string
	}{
		{handler: "append_a"}, {handler: "reject", want: "fixture rejection"}, {handler: "missing", want: "not registered"}, {handler: "panic", want: "panicked"},
	} {
		payload, err := msgpack.Marshal(abi.HookRequest{Hook: abi.HookRequestBefore, Handler: tc.handler, Request: abi.Request{Body: []byte("x")}})
		if err != nil {
			t.Fatal(err)
		}
		response, err := inst.InvokeHandleHook(t.Context(), payload)
		if err != nil {
			t.Fatal(err)
		}
		var result abi.HookResult
		if err := msgpack.Unmarshal(response, &result); err != nil {
			t.Fatal(err)
		}

		if tc.want != "" && !strings.Contains(result.Error, tc.want) {
			t.Fatalf("handler %s: %+v", tc.handler, result)
		}
		if tc.want == "" && (result.Request == nil || string(result.Request.Body) != "xa") {
			t.Fatalf("transform result: %+v", result)
		}
		count, err := inst.Module().ExportedFunction("deallocation_count").Call(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if count[0] != uint64(2*(i+1)) {
			t.Fatalf("deallocations = %d, want %d", count[0], 2*(i+1))
		}
	}
}

func TestInvokeHandleHookMissingExport(t *testing.T) {
	inst := newInstanceForTest(t, handleActivityEchoModule)
	_, err := inst.InvokeHandleHook(t.Context(), []byte("payload"))
	if err == nil || !strings.Contains(err.Error(), "handle_hook") {
		t.Fatalf("error = %v", err)
	}
}
