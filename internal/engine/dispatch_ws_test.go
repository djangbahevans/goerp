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
	"github.com/djangbahevans/goerp/internal/engine/role"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/ws"
)

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

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
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

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
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
		reached, err := hub.BroadcastUser(ctx, "notifications", "tenant-1", "user-1", "notification.new", nil)
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

func TestDispatchWSRoute_RoleSubscriptionsUseTenantDatabaseGrants(t *testing.T) {
	for _, mutation := range []string{"revoke", "expire", "suspend"} {
		t.Run(mutation, func(t *testing.T) {
			f := newChainFixture(t)
			hub := ws.NewHub()
			e := &Engine{wsHub: hub, roleStore: role.NewStore(f.conn)}
			url := wsTestServer(t, e,
				&authcheck.AuthContext{IsAuthenticated: true, UserID: f.userID, RolesLive: []string{"admin", "portal"}},
				&tenantresolve.TenantContext{TenantID: f.tenantID, Slug: f.tenantSlug})

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.CloseNow() }()

			send := func(channel string) {
				t.Helper()
				if err := wsjson.Write(ctx, conn, map[string]string{"type": "subscribe", "channel": channel}); err != nil {
					t.Fatal(err)
				}
			}
			readType := func(want string) {
				t.Helper()
				var env map[string]any
				if err := wsjson.Read(ctx, conn, &env); err != nil {
					t.Fatal(err)
				}
				if env["type"] != want {
					t.Fatalf("message = %v, want type %s", env, want)
				}
			}
			const channel = "ui:role:admin"
			send(channel)
			// The refused frame synchronizes prior admission without trusting cached roles.
			send("ui:role:portal")
			readType("subscription.denied")
			if n, err := hub.BroadcastTenant(ctx, channel, f.tenantID, "before", nil); err != nil || n != 1 {
				t.Fatalf("live admin role reached %d: %v", n, err)
			}
			readType("before")

			schema := tenantschema.Name(f.tenantSlug)
			query := "DELETE FROM " + schema + ".user_roles WHERE user_id = $1"
			switch mutation {
			case "expire":
				query = "UPDATE " + schema + ".user_roles SET expires_at = NOW() - INTERVAL '1 second' WHERE user_id = $1"
			case "suspend":
				query = "UPDATE " + schema + ".tenant_members SET status = 'suspended' WHERE user_id = $1"
			}
			if _, err := f.conn.ExecContext(ctx, query, f.userID); err != nil {
				t.Fatal(err)
			}
			if n, err := hub.BroadcastTenant(ctx, channel, f.tenantID, "after", nil); err != nil || n != 0 {
				t.Fatalf("removed admin role reached %d: %v", n, err)
			}
			send(channel)
			readType("subscription.denied")
		})
	}
}
