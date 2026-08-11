package adminapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNewServerEmptyTokenFails(t *testing.T) {
	_, err := NewServer(&Config{ListenAddr: "127.0.0.1:0", AdminToken: ""})
	if !errors.Is(err, ErrEmptyAdminToken) {
		t.Fatalf("NewServer with empty AdminToken: error = %v, want %v", err, ErrEmptyAdminToken)
	}
}

func TestNewServerRegisteredRouteRequiresAuth(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a test port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	s, err := NewServer(&Config{ListenAddr: addr, AdminToken: "correct-token"})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}

	s.Router().HandleFunc("GET /admin/_test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	go func() {
		if err := s.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Start() error: %v", err)
		}
	}()
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error: %v", err)
		}
	})

	url := "http://" + addr + "/admin/_test"

	waitForServer(t, url)

	cases := []struct {
		name       string
		authHeader string
		want       int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer wrong-token", http.StatusUnauthorized},
		{"correct token", "Bearer correct-token", http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if c.authHeader != "" {
				req.Header.Set("Authorization", c.authHeader)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != c.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, c.want)
			}
		})
	}
}

// waitForServer polls url until it responds at all (any status code),
// tolerating the brief window between starting the listener goroutine and
// it actually accepting connections.
func waitForServer(t *testing.T, url string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server never became reachable: %v", lastErr)
}
