package engine

import (
	"bufio"
	"encoding/json/v2"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestRouteResolutionMiddleware_RawRequestPaths(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{}); err != nil {
		t.Fatalf("update registry: %v", err)
	}

	// Nil dependencies expose any attempt to resolve a tenant or authenticate
	// requests that must be rejected during route resolution.
	h := buildChain(&Engine{}, reg, nil, nil, nil, nil, noop.NewTracerProvider().Tracer("test"), nil, route.RateLimitConfig{})
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	tests := []struct {
		name       string
		target     string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "dot segment",
			target:     "/contacts/.",
			wantStatus: http.StatusBadRequest,
			wantCode:   "bad_path",
		},
		{
			name:       "dot dot segment",
			target:     "/contacts/..",
			wantStatus: http.StatusBadRequest,
			wantCode:   "bad_path",
		},
		{
			name:       "embedded dot dot segment",
			target:     "/contacts/../_health",
			wantStatus: http.StatusBadRequest,
			wantCode:   "bad_path",
		},
		{
			name:       "dot segment after duplicate slashes",
			target:     "/contacts//./",
			wantStatus: http.StatusBadRequest,
			wantCode:   "bad_path",
		},
		{
			name:       "unmatched valid path",
			target:     "/contacts/missing",
			wantStatus: http.StatusNotFound,
			wantCode:   "route_not_found",
		},
		{
			name:       "malformed percent escape",
			target:     "/a/%zz",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var dialer net.Dialer
			conn, err := dialer.DialContext(t.Context(), "tcp", server.Listener.Addr().String())
			if err != nil {
				t.Fatalf("connect to server: %v", err)
			}
			t.Cleanup(func() { _ = conn.Close() })

			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatalf("set connection deadline: %v", err)
			}
			if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: unresolved.invalid\r\nConnection: close\r\n\r\n", tc.target); err != nil {
				t.Fatalf("write raw request: %v", err)
			}

			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", resp.StatusCode, tc.wantStatus, body)
			}
			if tc.wantCode == "" {
				if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") || resp.Header.Get(requestIDHeader) != "" {
					t.Fatalf("headers = %v, want a parser rejection before engine middleware", resp.Header)
				}
				return
			}

			var env httperr.Envelope
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatalf("decode error envelope: %v; body: %s", err, body)
			}

			if env.Error.Code != tc.wantCode {
				t.Errorf("error.code = %q, want %q", env.Error.Code, tc.wantCode)
			}
			if env.Error.RequestID == "" || env.Error.RequestID != resp.Header.Get(requestIDHeader) {
				t.Errorf("request_id = %q, want response X-Request-Id %q", env.Error.RequestID, resp.Header.Get(requestIDHeader))
			}
		})
	}
}
