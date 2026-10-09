package adminapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testDevBootstrap struct {
	module string
	err    error
}

func (b *testDevBootstrap) BootstrapDev(_ context.Context, module string) (DevSession, error) {
	b.module = module
	return DevSession{TenantID: "dev-id", Email: "admin@dev.localhost", Password: "dev-password"}, b.err
}

func TestDevRouteGatesAndAuth(t *testing.T) {
	for _, cfg := range []struct {
		enabled bool
		env     string
		domain  string
	}{
		{false, "development", "localhost"},
		{true, "production", "localhost"},
		{true, "staging", "localhost"},
		{true, "development", "goerp.io"},
	} {
		mux := http.NewServeMux()
		bootstrap := &testDevBootstrap{}
		RegisterDevRoute(mux, cfg.enabled, cfg.env, cfg.domain, bootstrap)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/dev/bootstrap", strings.NewReader(`{"module":"demo"}`)))
		if recorder.Code != http.StatusNotFound || bootstrap.module != "" {
			t.Fatalf("dev route exposed for %+v", cfg)
		}
	}

	bootstrap := &testDevBootstrap{}
	mux := http.NewServeMux()
	RegisterDevRoute(mux, true, "development", "localhost", bootstrap)
	handler := adminAuthMiddleware("token")(mux)
	for _, token := range []string{"", "wrong", "token"} {
		req := httptest.NewRequest(http.MethodPost, "/admin/dev/bootstrap", strings.NewReader(`{"module":"demo"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if token != "token" {
			if recorder.Code != http.StatusUnauthorized || bootstrap.module != "" {
				t.Fatal("unauthorized dev bootstrap reached the engine")
			}
			continue
		}
		if recorder.Code != http.StatusOK || bootstrap.module != "demo" || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("authorized bootstrap: %d %s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestDevRouteValidationAndFailure(t *testing.T) {
	bootstrap := &testDevBootstrap{err: errors.New("module not loaded")}
	mux := http.NewServeMux()
	RegisterDevRoute(mux, true, "development", "localhost", bootstrap)
	for _, body := range []string{`{`, `{}`, `{"module":"demo"}`} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/admin/dev/bootstrap", strings.NewReader(body)))
		want := http.StatusBadRequest
		if strings.Contains(body, "demo") {
			want = http.StatusConflict
		}
		if recorder.Code != want {
			t.Fatalf("body %s: status %d, want %d", body, recorder.Code, want)
		}
	}
}
