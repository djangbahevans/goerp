package engine

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/httpx"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
)

func TestAdminHealthIndependentOfApplicationReadiness(t *testing.T) {
	cfg := baseTestConfig(t)
	cfg.ModuleDev = true
	cfg.ModuleDir = t.TempDir()
	cfg.AdminMaxConcurrent = 10
	cfg.ListenAddr = shutdownTestAddress(t)
	cfg.AdminAddr = shutdownTestAddress(t)

	e, err := New(cfg)
	requireEngineConstruction(t, err)
	closeTestEnginePools(t, e)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := e.Shutdown(ctx); err != nil {
			t.Errorf("shutdown engine: %v", err)
		}
	})

	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	for _, tc := range []struct {
		name         string
		ready        bool
		failedModule bool
	}{
		{name: "application ready", ready: true},
		{name: "failed module", ready: true, failedModule: true},
		{name: "application not ready", failedModule: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e.readiness.Store(tc.ready)
			if tc.failedModule {
				if _, err := e.moduleRegistry.Update(map[string]*module.LoadedModule{
					"probe": {
						Manifest:      manifest.Manifest{Name: "probe", Type: "standard"},
						Status:        module.StatusFailed,
						FailureReason: "module unavailable",
					},
				}); err != nil {
					t.Fatal(err)
				}
			}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+cfg.ListenAddr+"/_ready", nil)
			if err != nil {
				t.Fatal(err)
			}

			main, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer main.Body.Close()

			wantStatus := http.StatusOK
			if !tc.ready {
				wantStatus = http.StatusServiceUnavailable
			}
			if main.StatusCode != wantStatus {
				t.Fatalf("main readiness status = %d, want %d", main.StatusCode, wantStatus)
			}

			var readiness httpx.ReadyReport
			if err := json.UnmarshalRead(main.Body, &readiness); err != nil {
				t.Fatal(err)
			}
			if readiness.Ready != tc.ready || (tc.failedModule && readiness.Modules.Failed != 1) {
				t.Fatalf("main readiness did not reflect application state: %+v", readiness)
			}

			req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+cfg.AdminAddr+"/admin/health", nil)
			if err != nil {
				t.Fatal(err)
			}

			admin, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Body.Close()

			body, err := io.ReadAll(admin.Body)
			if err != nil {
				t.Fatal(err)
			}
			if admin.StatusCode != http.StatusOK || string(body) != `{"data":{"status":"ok"},"error":null}` {
				t.Errorf("admin health = %d %s, want 200 with status ok", admin.StatusCode, body)
			}
		})
	}
}
