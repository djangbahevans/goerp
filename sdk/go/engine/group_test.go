package engine

import (
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func okHandler(*Request) *Response { return &Response{StatusCode: 200} }

func declaration(t *testing.T, r *Router, method, path string) RouteDeclaration {
	t.Helper()
	for _, d := range routeDeclarations(r.routes) {
		if d.Method == method && d.Path == path {
			return d
		}
	}
	t.Fatalf("no %s %s route in %+v", method, path, routeDeclarations(r.routes))
	return RouteDeclaration{}
}

func TestGroup_ComposesPrefixAndPattern(t *testing.T) {
	r := withRouter(t)
	orders := Group("/orders")
	orders.GET("", okHandler)
	orders.GET("/{id}", okHandler)
	orders.POST("{id}/confirm", okHandler)
	Group("orders/").PUT("/{id}", okHandler)

	for _, want := range []struct{ method, path string }{
		{"GET", "/orders"}, {"GET", "/orders/{id}"}, {"POST", "/orders/{id}/confirm"}, {"PUT", "/orders/{id}"},
	} {
		declaration(t, r, want.method, want.path)
	}
}

func TestGroup_RoutesAreServedAtTheirComposedPath(t *testing.T) {
	r := withRouter(t)
	var gotID string
	Group("/orders").GET("/{id}", func(req *Request) *Response {
		gotID = req.PathParams["id"]
		return &Response{StatusCode: 200}
	})

	if resp := r.Handle(&Request{Method: "GET", Path: "/orders/o-1"}); resp.StatusCode != 200 || gotID != "o-1" {
		t.Errorf("status = %d, id = %q, want 200 and o-1", resp.StatusCode, gotID)
	}
	if resp := r.Handle(&Request{Method: "GET", Path: "/o-1"}); resp.StatusCode != 404 {
		t.Errorf("an unprefixed path = %d, want 404", resp.StatusCode)
	}
}

func TestGroup_MiddlewareIsDeclaredOnEveryRoute(t *testing.T) {
	r := withRouter(t)
	read := perm.Ref("sales:order:read")
	api := Group("/orders", Requires(read), RateLimit(10, 60, PerUser), MaxBody(1024), Timeout(5*time.Second))
	api.GET("", okHandler)
	api.POST("", okHandler)

	for _, method := range []string{"GET", "POST"} {
		d := declaration(t, r, method, "/orders")
		if !slices.Equal(d.Permissions, []string{"sales:order:read"}) {
			t.Errorf("%s permissions = %v, want the group's", method, d.Permissions)
		}
		if d.RateLimit == nil || d.RateLimit.Requests != 10 || d.RateLimit.WindowSeconds != 60 {
			t.Errorf("%s rate limit = %+v, want 10 per 60s", method, d.RateLimit)
		}
		if d.MaxBodyBytes != 1024 || d.TimeoutMs != 5000 {
			t.Errorf("%s max body / timeout = %d / %d, want 1024 / 5000", method, d.MaxBodyBytes, d.TimeoutMs)
		}
		if d.Auth != string(AuthRequired) {
			t.Errorf("%s auth = %q, want required", method, d.Auth)
		}
	}
}

func TestGroup_RouteOptionsOverrideTheGroupsAndPermissionsAccumulate(t *testing.T) {
	r := withRouter(t)
	api := Group("/orders", RequireAuth(), Requires(perm.Ref("sales:order:read")), MaxBody(1024), Timeout(5*time.Second))
	api.POST("", okHandler, Requires(perm.Ref("sales:order:write")), MaxBody(2048))
	api.GET("/public", okHandler, Auth(AuthNone))

	post := declaration(t, r, "POST", "/orders")
	if !slices.Equal(post.Permissions, []string{"sales:order:read", "sales:order:write"}) {
		t.Errorf("permissions = %v, want the group's then the route's", post.Permissions)
	}
	if post.MaxBodyBytes != 2048 || post.TimeoutMs != 5000 {
		t.Errorf("max body / timeout = %d / %d, want the route's 2048 and the group's 5000", post.MaxBodyBytes, post.TimeoutMs)
	}
	if got := declaration(t, r, "GET", "/orders/public").Auth; got != string(AuthNone) {
		t.Errorf("auth = %q, want the route's own none", got)
	}
}

func TestGroup_NestedGroupExtendsPrefixAndMiddleware(t *testing.T) {
	r := withRouter(t)
	orders := Group("/orders", Requires(perm.Ref("sales:order:read")), Timeout(5*time.Second))
	lines := orders.Group("/{id}/lines", Requires(perm.Ref("sales:line:read")), Timeout(9*time.Second))
	lines.GET("", okHandler)
	orders.GET("", okHandler)

	nested := declaration(t, r, "GET", "/orders/{id}/lines")
	if !slices.Equal(nested.Permissions, []string{"sales:order:read", "sales:line:read"}) || nested.TimeoutMs != 9000 {
		t.Errorf("nested = %v / %dms, want both groups' permissions and the inner timeout", nested.Permissions, nested.TimeoutMs)
	}
	if outer := declaration(t, r, "GET", "/orders"); len(outer.Permissions) != 1 || outer.TimeoutMs != 5000 {
		t.Errorf("the outer group's route = %v / %dms, want it unaffected by the nested group", outer.Permissions, outer.TimeoutMs)
	}
}

func TestGroup_WebsocketAndSSERoutes(t *testing.T) {
	r := withRouter(t)
	g := Group("/live", Requires(perm.Ref("sales:order:read")))
	g.WS("/orders", okHandler)
	g.SSE("/feed", okHandler)

	if d := declaration(t, r, "GET", "/live/orders"); !d.Websocket || len(d.Permissions) != 1 {
		t.Errorf("WS route = %+v, want a guarded websocket", d)
	}
	if d := declaration(t, r, "GET", "/live/feed"); !d.Streaming || len(d.Permissions) != 1 {
		t.Errorf("SSE route = %+v, want a guarded streaming route", d)
	}
}

func TestGroup_DoesNotShareMiddlewareBetweenGroups(t *testing.T) {
	r := withRouter(t)
	base := Group("/a", Timeout(5*time.Second))
	base.Group("/x", Timeout(6*time.Second)).GET("", okHandler)
	base.Group("/y").GET("", okHandler)

	if got := declaration(t, r, "GET", "/a/y").TimeoutMs; got != 5000 {
		t.Errorf("sibling group timeout = %dms, want 5000: a nested group leaked its middleware", got)
	}
}
