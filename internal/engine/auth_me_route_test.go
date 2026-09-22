package engine

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/auth/authme"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

// TestBuildChain_AuthMeReachesHandlerThroughRealRouteTable guards
// goerp#822 — representative for /auth/refresh, /auth/logout, and
// /admin/tenant/plan too, which share the same fix.
func TestBuildChain_AuthMeReachesHandlerThroughRealRouteTable(t *testing.T) {
	f := newChainFixture(t)

	userStore := user.NewStore(f.conn)
	authMeHandler := authme.NewHandler(f.resolver, f.checker, userStore, nil, nil)
	h := f.chain(map[string]http.Handler{"GET /auth/me": authMeHandler})

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Host = f.domain
	req.Header.Set("Authorization", "Bearer "+f.issueToken(t))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.User.ID != f.userID {
		t.Errorf("user.id = %q, want %q", resp.User.ID, f.userID)
	}
}
