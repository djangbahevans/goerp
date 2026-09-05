package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/ws"
)

// wsTestServer wires a real *Engine's dispatchWSRoute behind a real
// listening HTTP server, injecting the given auth/tenant context the way
// the real middleware chain would — a direct-call unit test, the same
// pattern dispatchSharesFixture.request uses elsewhere in this package,
// since a WebSocket upgrade needs a real network connection (Hijack) and
// can't be exercised through httptest.NewRecorder.
func wsTestServer(t *testing.T, e *Engine, authCtx *authcheck.AuthContext, tenantCtx *tenantresolve.TenantContext) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if tenantCtx != nil {
			ctx = withTenantContext(ctx, tenantCtx)
		}
		if authCtx != nil {
			ctx = withAuthContext(ctx, authCtx)
		}
		e.dispatchWSRoute(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)
	return "ws://" + srv.Listener.Addr().String()
}

func TestDispatchWSRoute_UnresolvedAuthOrTenantRejectsBeforeUpgrade(t *testing.T) {
	e := &Engine{wsHub: ws.NewHub()}
	url := wsTestServer(t, e, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, nil)
	if err == nil {
		t.Fatal("expected Dial to fail — no auth/tenant context resolved")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		status := -1
		if resp != nil {
			status = resp.StatusCode
		}
		t.Errorf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
}

func TestDispatchWSRoute_AuthenticatedRequestUpgradesAndRegistersWithHub(t *testing.T) {
	hub := ws.NewHub()
	e := &Engine{wsHub: hub}
	authCtx := &authcheck.AuthContext{IsAuthenticated: true, UserID: "user-1"}
	tenantCtx := &tenantresolve.TenantContext{TenantID: "tenant-1"}
	url := wsTestServer(t, e, authCtx, tenantCtx)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	if err := wsjson.Write(ctx, conn, map[string]string{"type": "subscribe", "channel": "notifications"}); err != nil {
		t.Fatalf("subscribe write: %v", err)
	}

	// The server's own read loop processes that subscribe message
	// asynchronously — poll Broadcast until it actually reaches the new
	// subscriber, instead of racing a single fixed-delay attempt.
	deadline := time.Now().Add(5 * time.Second)
	for {
		reached, err := hub.Broadcast(ctx, "notifications", "notification.new", nil)
		if err != nil {
			t.Fatalf("Broadcast: %v", err)
		}
		if reached > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Broadcast never reached the subscribed connection in time")
		}
		time.Sleep(5 * time.Millisecond)
	}

	var env map[string]any
	if err := wsjson.Read(ctx, conn, &env); err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	if env["channel"] != "notifications" {
		t.Errorf("channel = %v, want notifications", env["channel"])
	}
}
