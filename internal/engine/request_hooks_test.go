package engine

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	hostabi "github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"go.opentelemetry.io/otel/trace"
)

func TestRequestHooksRealDispatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", path, "./wasm/testdata/hookfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := wasm.New(&config.Config{CompilationCache: wasmtest.SharedCompilationCacheDir(), PoolMaxMemoryByes: 64 << 20, Environment: string(config.Production)}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	resolutions := 0
	rt.SetLocaleResolver(func(context.Context, string, string) (abi.LocaleInfo, error) {
		resolutions++
		return abi.LocaleInfo{Locale: "fr", Timezone: "Africa/Accra", Currency: "GHS", Direction: "ltr"}, nil
	})

	compiled, err := rt.CompileModule(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
	mods := make(map[string]*module.LoadedModule)
	for _, name := range []string{"a", "z"} {
		pool := rt.NewPool(name, compiled, wasm.PoolConfig{MaxSize: 1, WarmSize: 1, BorrowTimeout: time.Second})
		t.Cleanup(func() { pool.DrainAndClose(context.Background(), 5*time.Second) })
		mods[name] = &module.LoadedModule{Status: module.StatusReady, Manifest: manifest.Manifest{Name: name, Type: "domain"}, Pool: pool}
	}
	mods["a"].Manifest.Hooks = []manifest.Hook{
		{Hook: abi.HookRequestBefore, Handler: "append_c", Priority: new(10)},
		{Hook: abi.HookRequestBefore, Handler: "append_a"},
		{Hook: abi.HookRequestAfter, Handler: "after"},
	}
	mods["z"].Manifest.Hooks = []manifest.Hook{{Hook: abi.HookRequestBefore, Handler: "append_b"}}
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(mods); err != nil {
		t.Fatal(err)
	}
	e := &Engine{wasmRuntime: rt, moduleRegistry: reg}

	dispatch := func(t *testing.T) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/a/echo", strings.NewReader("x"))
		ctx := withAuthContext(r.Context(), &authcheck.AuthContext{IsAuthenticated: true, UserID: "actor"})
		ctx = withTenantContext(ctx, &tenantresolve.TenantContext{TenantID: "tenant", Slug: "slug"})
		ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}}))
		r = r.WithContext(ctx)
		rr := &routeResolution{snap: reg.Snapshot(), entry: &route.RouteEntry{ModuleName: "a", PathTemplate: "/a/echo"}}
		w := httptest.NewRecorder()
		before := resolutions
		e.dispatchModuleRoute(ctx, w, r, rr, mods["a"])
		if resolutions != before+1 {
			t.Fatalf("locale resolutions = %d, want one per request", resolutions-before)
		}
		return w
	}
	w := dispatch(t)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["body"] != "xabc" || body["user"] != "actor" || body["tenant"] != "tenant" || body["trace"] != (trace.TraceID{1}).String() || body["path"] != "/echo" || body["authorization"] != "" || body["cookie"] != "" || body["region"] != "GH" {
		t.Fatalf("body = %v", body)
	}
	if w.Header().Get("X-Hook") != "after" {
		t.Fatal("after hook did not transform response")
	}

	t.Run("locale transform", func(t *testing.T) {
		mods["a"].Manifest.Hooks = []manifest.Hook{{Hook: abi.HookRequestBefore, Handler: "locale"}}
		mods["z"].Manifest.Hooks = nil
		if _, err := reg.Update(mods); err != nil {
			t.Fatal(err)
		}
		w := dispatch(t)
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || body["locale"] != "he" || body["timezone"] != "Asia/Jerusalem" || body["currency"] != "ILS" || body["direction"] != "rtl" {
			t.Fatalf("transformed locale response = %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("native CRUD", func(t *testing.T) {
		f := newDispatchORMFixture(t)
		mods["a"].Manifest.Hooks = []manifest.Hook{{Hook: abi.HookRequestBefore, Handler: "native"}, {Hook: abi.HookRequestAfter, Handler: "after"}}
		mods["z"].Manifest.Hooks = nil
		mods["testmodule"] = f.e.moduleRegistry.Snapshot().Modules()["testmodule"]
		if _, err := reg.Update(mods); err != nil {
			t.Fatal(err)
		}
		f.e.moduleRegistry = reg
		f.e.wasmRuntime = rt
		w := httptest.NewRecorder()
		f.e.buildDispatchHandler(nil).ServeHTTP(w, f.request(http.MethodPost, "/testmodule/widgets", []byte(`{"name":"Original"}`), f.entryCreate, nil))
		if w.Code != 201 || w.Header().Get("X-Hook") != "after" || !strings.Contains(w.Body.String(), `"name":"Hook widget"`) {
			t.Fatalf("native response = %d: %s", w.Code, w.Body.String())
		}
		w = httptest.NewRecorder()
		missingID := "00000000-0000-0000-0000-0000000000ff"
		f.e.buildDispatchHandler(nil).ServeHTTP(w, f.request(http.MethodGet, "/testmodule/widgets/"+missingID, nil, f.entryGet, map[string]string{"id": missingID}))
		if w.Code != 404 || w.Header().Get("X-Hook") != "after" {
			t.Fatalf("native error response = %d: %s", w.Code, w.Body.String())
		}
		delete(mods, "testmodule")
	})

	mods["a"].Manifest.Hooks = []manifest.Hook{{Hook: abi.HookRequestAfter, Handler: "after"}}
	if _, err := reg.Update(mods); err != nil {
		t.Fatal(err)
	}
	original := EngineResponse{StatusCode: 422, Body: []byte(`{ "error": {"code": "orm.validation_failed"} }` + "\n")}
	preserved, err := e.afterRequestHooks(t.Context(), reg.Snapshot(), EngineRequest{}, original)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.StatusCode != original.StatusCode || string(preserved.Body) != string(original.Body) {
		t.Fatalf("classified response changed: %+v", preserved)
	}

	mods["a"].Capabilities = hostabi.CapAuthzCheck
	mods["a"].Manifest.Permissions = []manifest.Permission{{Name: "a:record:read"}}
	mods["a"].Manifest.Hooks = []manifest.Hook{{Hook: abi.HookRequestBefore, Handler: "authorized"}}
	if _, err := reg.Update(mods); err != nil {
		t.Fatal(err)
	}
	var permissions permission.PermissionBitfield
	index, ok := reg.Snapshot().PermissionRegistry().Index("a:record:read")
	if !ok {
		t.Fatal("permission not registered")
	}
	permissions.Set(index)
	if _, err := e.beforeRequestHooks(t.Context(), reg.Snapshot(), EngineRequest{UserID: "actor", PermissionSet: permissions}); err != nil {
		t.Fatalf("host permission context: %v", err)
	}
	if _, err := e.beforeRequestHooks(t.Context(), reg.Snapshot(), EngineRequest{UserID: "actor"}); err == nil {
		t.Fatal("hook gained a permission absent from the caller")
	}

	mods["a"].Manifest.Hooks = []manifest.Hook{{Hook: abi.HookRequestBefore, Handler: "reject"}}
	if _, err := reg.Update(mods); err != nil {
		t.Fatal(err)
	}
	w = dispatch(t)
	if w.Code != 500 || strings.Contains(w.Body.String(), "fixture rejection") {
		t.Fatalf("hook rejection = %d: %s", w.Code, w.Body.String())
	}

	mods["a"].Manifest.Hooks = []manifest.Hook{{Hook: abi.HookRequestAfter, Handler: "after_change"}}
	if _, err := reg.Update(mods); err != nil {
		t.Fatal(err)
	}
	w = dispatch(t)
	if w.Code != 202 || w.Body.String() != `{"changed":true}` || w.Header().Get("Content-Length") != "" {
		t.Fatalf("replacement response = %d: %s", w.Code, w.Body.String())
	}

	mods["a"].Manifest.Hooks = []manifest.Hook{{Hook: abi.HookRequestAfter, Handler: "reject"}}
	if _, err := reg.Update(mods); err != nil {
		t.Fatal(err)
	}
	w = dispatch(t)
	if w.Code != 500 || strings.Contains(w.Body.String(), "fixture rejection") {
		t.Fatalf("after-hook rejection = %d: %s", w.Code, w.Body.String())
	}

	mods["a"].Manifest.Hooks = nil
	mods["z"].Manifest.Hooks = nil
	if _, err := reg.Update(mods); err != nil {
		t.Fatal(err)
	}
	w = dispatch(t)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"body":"x"`) || w.Header().Get("X-Hook") != "" {
		t.Fatalf("no-hook response = %d: %s", w.Code, w.Body.String())
	}
}
