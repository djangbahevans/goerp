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
	"uuid"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
)

func TestRequestHookErrorTranslations(t *testing.T) {
	name := "localized_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	store := errorTranslationStore(t, name)
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
	cleanupCtx := context.WithoutCancel(t.Context())
	t.Cleanup(func() { _ = rt.Close(cleanupCtx) })
	rt.SetLocaleResolver(func(context.Context, string, string) (abi.LocaleInfo, error) {
		return abi.LocaleInfo{Locale: "fr", Timezone: "UTC"}, nil
	})
	compiled, err := rt.CompileModule(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(cleanupCtx) })
	pool := rt.NewPool(name, compiled, wasm.PoolConfig{MaxSize: 1, BorrowTimeout: time.Second})
	t.Cleanup(func() { pool.DrainAndClose(cleanupCtx, 5*time.Second) })
	code := name + ".contact.not_found"
	mod := &module.LoadedModule{
		Status: module.StatusReady,
		Manifest: manifest.Manifest{Name: name, Type: "domain", Hooks: []manifest.Hook{
			{Hook: abi.HookRequestBefore, Handler: "locale"},
			{Hook: abi.HookRequestAfter, Handler: "after_error"},
		}},
		ErrorCodes: map[string]struct{}{code: {}},
		Pool:       pool,
	}
	reg := &registry.ModuleRegistry{Translations: store}
	if _, err := reg.Update(map[string]*module.LoadedModule{name: mod}); err != nil {
		t.Fatal(err)
	}
	e := &Engine{wasmRuntime: rt, moduleRegistry: reg}
	ctx := withTenantContext(t.Context(), &tenantresolve.TenantContext{TenantID: "tenant", Slug: "tenant"})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "user"})
	body := `{"error":{"code":"` + code + `","message":"handler message","details":{"id":"c1"}}}`
	r := httptest.NewRequest(http.MethodPost, "/"+name+"/echo", strings.NewReader(body)).WithContext(ctx)
	w := httptest.NewRecorder()
	e.dispatchModuleRoute(ctx, w, r, &routeResolution{snap: reg.Snapshot(), entry: &route.RouteEntry{ModuleName: name}}, mod)

	var got httperr.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 404 || got.Error.Code != code || got.Error.Message != "איש קשר לא נמצא" {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
	if details, ok := got.Error.Details.(map[string]any); !ok || details["id"] != "c1" {
		t.Fatalf("details = %+v", got.Error.Details)
	}
}
