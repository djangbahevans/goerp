package adminapi

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestHealthServingAndAuthentication(t *testing.T) {
	s, err := NewServer(&Config{
		ListenAddr:    "127.0.0.1:0",
		AdminToken:    "correct-token",
		MaxBodyBytes:  1 << 20,
		MaxConcurrent: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	s.Router().HandleFunc("GET /admin/_test", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}

	serveDone := make(chan error, 1)
	go func() { serveDone <- s.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("shutdown admin listener: %v", err)
		}

		select {
		case err := <-serveDone:
			if !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("serve admin listener: %v", err)
			}
		case <-ctx.Done():
			t.Error("admin listener did not stop")
		}
	})

	client := &http.Client{Timeout: 5 * time.Second}
	baseURL := "http://" + s.listener.Addr().String()

	cases := []struct {
		name   string
		method string
		path   string
		token  string
		status int
	}{
		{name: "health without token", method: http.MethodGet, path: "/admin/health", status: http.StatusOK},
		{name: "health with wrong token", method: http.MethodGet, path: "/admin/health", token: "wrong-token", status: http.StatusOK},
		{name: "health with token", method: http.MethodGet, path: "/admin/health", token: "correct-token", status: http.StatusOK},
		{name: "head health without token", method: http.MethodHead, path: "/admin/health", status: http.StatusOK},
		{name: "admin without token", method: http.MethodGet, path: "/admin/_test", status: http.StatusUnauthorized},
		{name: "admin with wrong token", method: http.MethodGet, path: "/admin/_test", token: "wrong-token", status: http.StatusUnauthorized},
		{name: "admin with token", method: http.MethodGet, path: "/admin/_test", token: "correct-token", status: http.StatusNoContent},
		{name: "mutating health without token", method: http.MethodPost, path: "/admin/health", status: http.StatusUnauthorized},
		{name: "health child without token", method: http.MethodGet, path: "/admin/health/child", status: http.StatusUnauthorized},
		{name: "unknown route without token", method: http.MethodGet, path: "/admin/unknown", status: http.StatusUnauthorized},
		{name: "main health without token", method: http.MethodGet, path: "/_health", status: http.StatusUnauthorized},
		{name: "main readiness without token", method: http.MethodGet, path: "/_ready", status: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), tc.method, baseURL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}

			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}

			req.Header.Set("Idempotency-Key", "probe")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}

			if tc.method != http.MethodGet || tc.status != http.StatusOK {
				return
			}

			if got := resp.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}

			var body struct {
				Data struct {
					Status string `json:"status"`
				} `json:"data"`
				Error any `json:"error"`
			}
			if err := json.UnmarshalRead(resp.Body, &body); err != nil {
				t.Fatal(err)
			}

			if body.Data.Status != "ok" || body.Error != nil {
				t.Errorf("health response = %+v, want status ok and null error", body)
			}
		})
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/admin/health", nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("health request succeeded after admin listener shutdown")
	}
}

func TestHealthUsesAdminConcurrencyLimit(t *testing.T) {
	s, err := NewServer(&Config{
		AdminToken:    "correct-token",
		MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() {
		unblock()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("blocking admin request did not finish")
		}
	})

	s.UnauthenticatedRouter().HandleFunc("GET /slow", func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	})
	go func() {
		defer close(done)
		s.http.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/slow", nil))
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking admin request did not start")
	}

	busy := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(busy, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/health", nil))
	if busy.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated health status = %d, want 503", busy.Code)
	}

	var body envelope
	if err := json.Unmarshal(busy.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error == nil || body.Error.Code != "admin_api_busy" {
		t.Errorf("saturated health response = %s, want admin_api_busy", busy.Body)
	}

	unblock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking admin request did not finish")
	}

	available := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(available, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/health", nil))
	if available.Code != http.StatusOK {
		t.Errorf("available health status = %d, want 200", available.Code)
	}
}
