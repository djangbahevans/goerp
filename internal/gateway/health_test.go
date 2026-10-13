package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/adminapi"
)

func TestGatewayAdminHealthRequiresBothCredentials(t *testing.T) {
	admin, err := adminapi.NewServer(&adminapi.Config{AdminToken: "correct-token"})
	if err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(admin.UnauthenticatedRouter())
	t.Cleanup(upstream.Close)

	direct, err := upstream.Client().Get(upstream.URL + "/admin/health")
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Body.Close()

	body, err := io.ReadAll(direct.Body)
	if err != nil {
		t.Fatal(err)
	}

	if direct.StatusCode != http.StatusOK {
		t.Fatalf("direct health status = %d, want 200", direct.StatusCode)
	}

	gw := setupTestGatewayWithUpstream(t, "correct-token", upstream)
	_, _, cert := gw.ca.leaf(t, "probe", false)

	if resp, err := clientFor().Get(gw.server.URL + "/admin/health"); err == nil {
		resp.Body.Close()
		t.Fatal("health request without a client certificate completed the TLS handshake")
	}

	client := clientFor(cert)
	for _, tc := range []struct {
		name   string
		token  string
		status int
	}{
		{name: "missing token", status: http.StatusUnauthorized},
		{name: "wrong token", token: "wrong-token", status: http.StatusUnauthorized},
		{name: "valid token", token: "correct-token", status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, gw.server.URL+"/admin/health", nil)
			if err != nil {
				t.Fatal(err)
			}

			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.status {
				t.Fatalf("gateway status = %d, want %d", resp.StatusCode, tc.status)
			}

			if tc.status == http.StatusOK {
				forwardedBody, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}

				if string(forwardedBody) != string(body) || resp.Header.Get("Content-Type") != direct.Header.Get("Content-Type") {
					t.Errorf("gateway changed the health response: %s", forwardedBody)
				}
			}
		})
	}
}
