package engine

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/sse"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	sdkengine "github.com/djangbahevans/goerp/sdk/go/engine"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestEventFramesValidation(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, `{"events":null,"done":false}`, `{"events":[],"done":"yes"}`,
		`{"events":[{}],"done":false}`, `{"events":[{"id":"bad\nid: forged","data":{}}],"done":false}`,
		`{"events":[{"data":"` + strings.Repeat("x", 64<<10) + `"}],"done":false}`,
	} {
		if _, _, _, err := eventFrames([]byte(body), "old"); err == nil {
			t.Fatalf("accepted %s", body[:min(len(body), 100)])
		}
	}
	events := make([]sdkengine.Event, 101)
	body, err := json.Marshal(map[string]any{"events": events, "done": false})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := eventFrames(body, "old"); err == nil {
		t.Fatal("accepted 101 events")
	}
	frames, done, cursor, err := eventFrames([]byte(`{"events":[{"id":"new","data":{"line": "one\ntwo"}},{"data":null}],"done":true}`), "old")
	if err != nil || !done || cursor != "new" || string(frames) != "id: new\ndata: {\"line\":\"one\\ntwo\"}\n\ndata: null\n\n" {
		t.Fatalf("frames=%q done=%v cursor=%s err=%v", frames, done, cursor, err)
	}
	frames, done, cursor, err = eventFrames([]byte(`{"events":[],"done":false}`), "client")
	if err != nil || done || len(frames) != 0 || cursor != "client" {
		t.Fatalf("empty frames=%q cursor=%s err=%v", frames, cursor, err)
	}
}

func TestSSEFailedPollsAndRecovery(t *testing.T) {
	state := ssePollState{cursor: "old"}
	for _, status := range []int{500, 503, 200, 500, 503} {
		body := []byte(`{"events":[],"done":false}`)
		if status == 200 {
			body = []byte(`{"events":[{"id":"new","data":1}],"done":false}`)
		}
		_, _, err := state.accept(EngineResponse{StatusCode: status, Body: body})
		if err != nil {
			t.Fatalf("status=%d err=%v", status, err)
		}
	}
	if state.cursor != "new" {
		t.Fatal(state.cursor)
	}
	if _, _, err := state.accept(EngineResponse{StatusCode: 200, Body: []byte(`{}`)}); err == nil {
		t.Fatal("third failed poll did not close")
	}
	state = ssePollState{cursor: "old"}
	if _, _, err := state.accept(EngineResponse{StatusCode: 403}); err == nil {
		t.Fatal("4xx did not close")
	}
}

func nextSSEData(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			return strings.TrimSpace(data)
		}
	}
}

func TestSSEHTTPPolling(t *testing.T) {
	e, mod := traceFixtureModule(t)
	mod.Manifest.Name = "widgets"
	mod.Manifest.Permissions = nil
	mod.ErrorCodes["tracefixture.stream.not_found"] = struct{}{}
	for i, mode := range []string{"normal", "reconnect", "first-done", "first-error", "later-error", "invalid", "timeout", "revoked", "permission", "removed", "disabled", "pool"} {
		t.Run(mode, func(t *testing.T) {
			f := newChainFixture(t)
			current := *mod
			current.Manifest.Permissions = f.reg.Snapshot().Modules()["widgets"].Manifest.Permissions
			current.ExplicitRoutes = []sdkengine.RouteDeclaration{{
				Method:         "GET",
				Path:           "/stream",
				Auth:           "required",
				Permissions:    []string{chainTestPermission},
				Streaming:      true,
				PollIntervalMs: 500,
				TimeoutMs:      250,
			}}
			if _, err := f.reg.Update(map[string]*module.LoadedModule{"widgets": &current}); err != nil {
				t.Fatal(err)
			}
			e.moduleRegistry = f.reg
			e.tenantResolver = f.resolver
			e.authChecker = f.checker
			e.streams = sse.New(5, 2, 10*time.Second)
			defer e.streams.Shutdown()
			ip := fmt.Sprintf("198.18.%d.%d", rand.IntN(256), i+1)
			t.Cleanup(func() { _ = f.cacheClient.Delete(context.Background(), "ratelimit:default:ip:"+ip) })
			handler := buildChain(e, f.reg, nil, []string{"127.0.0.1", "::1"}, f.resolver, f.checker, noop.NewTracerProvider().Tracer("test"), f.cacheClient, route.RateLimitConfig{Requests: 1, WindowSeconds: 60, Scope: "ip"})
			server := httptest.NewServer(handler)
			defer server.Close()
			server.Client().Timeout = 12 * time.Second
			req, err := http.NewRequestWithContext(t.Context(), "GET", server.URL+"/widgets/stream?mode="+mode+"&probe=query", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = f.domain
			req.Header.Set("Authorization", "Bearer "+f.issueToken(t))
			req.Header.Set("X-Probe", "header")
			req.Header.Set("X-Real-IP", ip)
			if mode == "reconnect" {
				req.Header.Set("Last-Event-ID", "client")
			}
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if mode == "first-error" {
				body, _ := io.ReadAll(response.Body)
				if response.StatusCode != 404 || response.Header.Get("Content-Type") != "application/json" || !strings.Contains(string(body), "tracefixture.stream.not_found") {
					t.Fatalf("response=%v body=%s", response, body)
				}
				return
			}
			if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("response=%v body=%s", response, body)
			}
			reader := bufio.NewReader(response.Body)
			after := ""
			if mode == "reconnect" {
				after = "client"
			}
			if first := nextSSEData(t, reader); !strings.Contains(first, `"after":"`+after+`"`) {
				t.Fatal(first)
			}

			inst, err := mod.Pool.Borrow(t.Context())
			if err != nil {
				t.Fatalf("instance held between polls: %v", err)
			}
			if mode == "pool" {
				defer mod.Pool.Return(inst)
			} else {
				mod.Pool.Return(inst)
			}
			switch mode {
			case "revoked":
				revoker := sessionrevoke.NewRevoker(session.NewStore(f.conn), f.cacheClient)
				if err := revoker.RevokeAllForUser(t.Context(), f.userID, "test"); err != nil {
					t.Fatal(err)
				}
			case "permission":
				if err := removeSSEFixtureRoles(t, f); err != nil {
					t.Fatal(err)
				}
				if err := permcache.NewRoleCache(f.cacheClient).Invalidate(t.Context(), f.tenantID, f.userID); err != nil {
					t.Fatal(err)
				}
			case "removed":
				current.ExplicitRoutes = nil
				if _, err := f.reg.Update(map[string]*module.LoadedModule{"widgets": &current}); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if _, err := f.conn.Exec(`INSERT INTO system.tenant_module_settings (tenant_id,module_name,enabled) VALUES ($1,'widgets',false) ON CONFLICT (tenant_id,module_name) DO UPDATE SET enabled=false`, f.tenantID); err != nil {
					t.Fatal(err)
				}
				if err := f.cacheClient.Delete(t.Context(), tenantresolve.EntitlementCacheKey(f.tenantID)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "normal" || mode == "reconnect" {
				second := nextSSEData(t, reader)
				if !strings.Contains(second, `"after":"one"`) || !strings.Contains(second, `"query":"query"`) || !strings.Contains(second, `"header":"header"`) || !strings.Contains(second, `"path":"/stream"`) {
					t.Fatal(second)
				}
			}
			rest, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(rest), "event: done") != (mode == "normal" || mode == "reconnect" || mode == "first-done") {
				t.Fatalf("mode=%s rest=%s", mode, rest)
			}
		})
	}
}

func removeSSEFixtureRoles(t *testing.T, f *chainFixture) error {
	t.Helper()
	_, err := f.conn.Exec("DELETE FROM "+tenantschema.Name(f.tenantSlug)+".user_roles WHERE user_id=$1", f.userID)
	return err
}
