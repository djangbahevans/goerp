package engine

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	sdk "github.com/djangbahevans/goerp/sdk/go/engine"
)

func TestDispatchModuleRoute_UIEnvelope(t *testing.T) {
	ctx := t.Context()
	wasmPath := filepath.Join(t.TempDir(), "uifixture.wasm")
	cmd := exec.CommandContext(ctx, "go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/uifixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build UI fixture: %v\n%s", err, out)
	}
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}

	rt, err := wasm.New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanupCtx := context.WithoutCancel(ctx)
	t.Cleanup(func() { _ = rt.Close(cleanupCtx) })

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(cleanupCtx) })

	pool := rt.NewPool("uifixture", compiled, wasm.PoolConfig{MaxSize: 1, BorrowTimeout: 5 * time.Second})
	t.Cleanup(func() { pool.DrainAndClose(cleanupCtx, 5*time.Second) })

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"uifixture": {
			Status:   module.StatusReady,
			Pool:     pool,
			Manifest: manifest.Manifest{Name: "uifixture", Type: "standard"},
			ExplicitRoutes: []sdk.RouteDeclaration{
				{Method: http.MethodPost, Path: "/confirm"},
				{Method: http.MethodPost, Path: "/instructions"},
				{Method: http.MethodPost, Path: "/plain"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	e := &Engine{wasmRuntime: rt}
	handler := routeResolutionMiddleware(reg)(e.buildDispatchHandler(nil))

	tests := []struct {
		path   string
		header string
		body   string
	}{
		{
			path:   "/confirm",
			header: "1",
			body: `{"data":{"state":"confirmed"},"ui":[
				{"type":"toast","message":"Order confirmed","severity":"success","duration_ms":0},
				{"type":"reload","resource":"sales.invoice","record_id":"inv1"}
			]}`,
		},
		{
			path:   "/instructions",
			header: "1",
			body:   `{"ui":[{"type":"close_modal"},{"type":"set_badge","target":"/sales/count","count":0}]}`,
		},
		{
			path: "/plain",
			body: `{"ui":"record field","data":"record data"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			ctx := withTenantContext(t.Context(), &tenantresolve.TenantContext{
				TenantID: "00000000-0000-0000-0000-000000000001",
				Slug:     "acme",
				Entitlements: tenantresolve.EntitlementSet{
					Features: map[string]bool{"module.uifixture": true},
				},
			})
			ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true})
			r := httptest.NewRequest(http.MethodPost, "/uifixture"+tt.path, nil).WithContext(ctx)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			if got := w.Header().Get("X-GoERP-UI"); got != tt.header {
				t.Errorf("X-GoERP-UI = %q, want %q", got, tt.header)
			}
			if got := w.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}

			var got, want any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.body), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("body = %s, want %s", w.Body.String(), tt.body)
			}
		})
	}
}
