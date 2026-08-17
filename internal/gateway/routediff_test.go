package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGateway_RouteDiff_MirrorsUpstreamExactly proves the gateway adds no
// routes of its own and removes none of the upstream's — for every path,
// gateway and upstream must agree, including 404s. This is goerp#180's
// verification that the gateway exposes no routes beyond the admin API's.
func TestGateway_RouteDiff_MirrorsUpstreamExactly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/tenants", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /admin/tenants/{slug}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /admin/tenants/{slug}/suspend", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	upstream := httptest.NewServer(mux)
	t.Cleanup(upstream.Close)

	gw := setupTestGatewayWithUpstream(t, "correct-token", upstream)
	_, _, clientCert := gw.ca.leaf(t, "operator-a", false)
	client := clientFor(clientCert)

	cases := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"registered exact route", http.MethodGet, "/admin/tenants", http.StatusOK},
		{"registered parameterized route", http.MethodGet, "/admin/tenants/acmecorp", http.StatusOK},
		{"registered mutating route", http.MethodPost, "/admin/tenants/acmecorp/suspend", http.StatusOK},
		{"unregistered admin-shaped path", http.MethodGet, "/admin/does-not-exist", http.StatusNotFound},
		{"root path", http.MethodGet, "/", http.StatusNotFound},
		{"no gateway-only health route", http.MethodGet, "/healthz", http.StatusNotFound},
		{"no gateway-only health route, admin-namespaced", http.MethodGet, "/admin/health", http.StatusNotFound},
		{"wrong method on a registered path", http.MethodDelete, "/admin/tenants", http.StatusMethodNotAllowed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// What the upstream itself returns, unmediated by the gateway —
			// the ground truth the gateway must match exactly.
			directReq := httptest.NewRequest(tc.method, tc.path, nil)
			directRec := httptest.NewRecorder()
			mux.ServeHTTP(directRec, directReq)

			if directRec.Code != tc.wantStatus {
				t.Fatalf("test setup: upstream directly returned %d for %s %s, want %d", directRec.Code, tc.method, tc.path, tc.wantStatus)
			}

			req, _ := http.NewRequest(tc.method, gw.server.URL+tc.path, nil)
			req.Header.Set("Authorization", "Bearer correct-token")

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Do() error: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != directRec.Code {
				t.Errorf("gateway returned %d for %s %s, want %d (upstream's own response)", resp.StatusCode, tc.method, tc.path, directRec.Code)
			}
		})
	}
}
