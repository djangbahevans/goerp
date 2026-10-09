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
	seeded bool
	err    error
}

func (b *testDevBootstrap) BootstrapDev(_ context.Context, module string) (DevSession, error) {
	b.module = module
	return DevSession{TenantID: "dev-id", Email: "admin@dev.localhost", Password: "dev-password"}, b.err
}

func (b *testDevBootstrap) SeedDev(_ context.Context, module string, batches []DevSeedBatch) error {
	b.module = module
	b.seeded = len(batches) == 1 && batches[0].Model == "demo.item"

	return b.err
}

func TestDevSeedRequiresLocalDevelopmentAndAuthentication(t *testing.T) {
	for _, cfg := range []struct {
		enabled bool
		env     string
		domain  string
	}{
		{false, "development", "localhost"},
		{true, "production", "localhost"},
		{true, "development", "example.com"},
		{true, "development", "localhost"},
	} {
		bootstrap := &testDevBootstrap{}
		mux := http.NewServeMux()
		RegisterDevRoute(mux, cfg.enabled, cfg.env, cfg.domain, bootstrap)
		handler := adminAuthMiddleware("token")(mux)

		for _, token := range []string{"wrong", "token"} {
			req := httptest.NewRequest(http.MethodPost, "/admin/dev/seed", strings.NewReader(`{"module":"demo","batches":[{"model":"demo.item","records":[{"name":"Example"}]}]}`))
			req.Header.Set("Authorization", "Bearer "+token)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)

			want := http.StatusUnauthorized
			if token == "token" {
				want = http.StatusNotFound
				if cfg.enabled && cfg.env == "development" && cfg.domain == "localhost" {
					want = http.StatusOK
				}
			}
			if recorder.Code != want || bootstrap.seeded != (want == http.StatusOK) {
				t.Fatalf("seed for %+v with token %q: status %d, seeded %t", cfg, token, recorder.Code, bootstrap.seeded)
			}
		}
	}
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

func TestDevReloadRequiresLocalDevelopmentAndAuthentication(t *testing.T) {
	for _, environment := range []string{"production", "staging", "development"} {
		called := false
		mux := http.NewServeMux()
		RegisterDevReloadRoute(mux, true, environment, "localhost", func(_ context.Context, name string, data []byte) error {
			called = true
			if name != "demo" || string(data) != "package" {
				t.Fatalf("reload request: %s %q", name, data)
			}
			return nil
		})
		handler := adminAuthMiddleware("token")(mux)
		for _, token := range []string{"wrong", "token"} {
			req := httptest.NewRequest(http.MethodPost, "/admin/dev/modules/demo/reload", strings.NewReader("package"))
			req.Header.Set("Authorization", "Bearer "+token)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)

			want := http.StatusUnauthorized
			if token == "token" {
				want = http.StatusNotFound
				if environment == "development" {
					want = http.StatusOK
				}
			}
			if recorder.Code != want {
				t.Fatalf("%s with %s: %d, want %d", environment, token, recorder.Code, want)
			}
			if called != (want == http.StatusOK) {
				t.Fatal("reload reached the module outside an authorized local development request")
			}
		}
	}
}
