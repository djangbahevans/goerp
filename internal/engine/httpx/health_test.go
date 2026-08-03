package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer() *Server {
	return NewServer(&Config{ListenAddr: ":0"}, nil)
}

func doRequest(t *testing.T, s *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(w, req)
	return w
}

func TestHandleHealthNoHealthFn(t *testing.T) {
	s := newTestServer()

	w := doRequest(t, s, http.MethodGet, "/_health")

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var report HealthReport
	if err := json.NewDecoder(w.Body).Decode(&report); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if report.Status != "healthy" {
		t.Errorf("Status = %q, want %q", report.Status, "healthy")
	}
}

func TestHandleHealthPrimaryDown(t *testing.T) {
	s := newTestServer()
	s.SetHealthFn(func(ctx context.Context) HealthReport {
		return HealthReport{
			Status: "degraded",
			Checks: map[string]CheckResult{
				"postgres_primary": {Status: "error", Error: "connection refused"},
				"redis":            {Status: "ok"},
			},
		}
	})

	w := doRequest(t, s, http.MethodGet, "/_health")

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d when postgres_primary is down", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleHealthOnlyRedisDown(t *testing.T) {
	// Per engine-internals.md §2 Stage 1, Redis is fail-hard for startup
	// but /_health's 503 is documented as postgres-primary-only — a
	// live-probe failure on anything else should report degraded, not 503.
	s := newTestServer()
	s.SetHealthFn(func(ctx context.Context) HealthReport {
		return HealthReport{
			Status: "degraded",
			Checks: map[string]CheckResult{
				"postgres_primary": {Status: "ok"},
				"redis":            {Status: "error", Error: "connection refused"},
			},
		}
	})

	w := doRequest(t, s, http.MethodGet, "/_health")

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d when only redis is down", w.Code, http.StatusOK)
	}

	var report HealthReport
	if err := json.NewDecoder(w.Body).Decode(&report); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if report.Status != "degraded" {
		t.Errorf("Status = %q, want %q", report.Status, "degraded")
	}
}

func TestHandleReadyNoReadyFn(t *testing.T) {
	s := newTestServer()

	w := doRequest(t, s, http.MethodGet, "/_ready")

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var report ReadyReport
	if err := json.NewDecoder(w.Body).Decode(&report); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !report.Ready {
		t.Error("Ready = false, want true when no readyFn is configured")
	}
}

func TestHandleReadySuccess(t *testing.T) {
	s := NewServer(&Config{ListenAddr: ":0"}, func(ctx context.Context) error { return nil })

	w := doRequest(t, s, http.MethodGet, "/_ready")

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleReadyFailure(t *testing.T) {
	s := NewServer(&Config{ListenAddr: ":0"}, func(ctx context.Context) error {
		return errors.New("engine is shutting down")
	})

	w := doRequest(t, s, http.MethodGet, "/_ready")

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var report ReadyReport
	if err := json.NewDecoder(w.Body).Decode(&report); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if report.Ready {
		t.Error("Ready = true, want false when readyFn returns an error")
	}
}

func TestProbeCheckSuccess(t *testing.T) {
	result := ProbeCheck(context.Background(), func(ctx context.Context) error { return nil })

	if result.Status != "ok" {
		t.Errorf("Status = %q, want %q", result.Status, "ok")
	}
	if result.Error != "" {
		t.Errorf("Error = %q, want empty", result.Error)
	}
	if result.LatencyMs < 0 {
		t.Errorf("LatencyMs = %v, want >= 0", result.LatencyMs)
	}
}

func TestProbeCheckFailure(t *testing.T) {
	result := ProbeCheck(context.Background(), func(ctx context.Context) error {
		return errors.New("boom")
	})

	if result.Status != "error" {
		t.Errorf("Status = %q, want %q", result.Status, "error")
	}
	if result.Error != "boom" {
		t.Errorf("Error = %q, want %q", result.Error, "boom")
	}
}
