package engine

import (
	"testing"
	"time"
)

func withFreshRouter(t *testing.T) {
	t.Helper()
	orig := DefaultRouter
	DefaultRouter = NewRouter()
	t.Cleanup(func() { DefaultRouter = orig })
}

func TestGET_AppliesOptionsToDeclaration(t *testing.T) {
	withFreshRouter(t)

	GET("/orders/{id}", func(req *Request) *Response { return OK(nil) },
		Auth(AuthOptional),
		Requires("sales:order:read"),
		RateLimit(100, 60, PerUser),
		Timeout(10*time.Second),
		MaxBody(65536),
		Embeds("lines", "sales.order_line", true),
		PathParam("id", UUIDParam),
	)

	decls := routeDeclarations(DefaultRouter.routes)
	if len(decls) != 1 {
		t.Fatalf("got %d decls, want 1", len(decls))
	}

	got := decls[0]
	if got.Auth != string(AuthOptional) {
		t.Errorf("Auth = %q, want %q", got.Auth, AuthOptional)
	}
	if len(got.Permissions) != 1 || got.Permissions[0] != "sales:order:read" {
		t.Errorf("Permissions = %+v, want [sales:order:read]", got.Permissions)
	}
	if got.RateLimit == nil || got.RateLimit.Requests != 100 {
		t.Errorf("RateLimit = %+v, want Requests=100", got.RateLimit)
	}
	if got.TimeoutMs != 10000 {
		t.Errorf("TimeoutMs = %d, want 10000", got.TimeoutMs)
	}
	if got.MaxBodyBytes != 65536 {
		t.Errorf("MaxBodyBytes = %d, want 65536", got.MaxBodyBytes)
	}
	if len(got.Embedded) != 1 || got.Embedded[0].Field != "lines" {
		t.Errorf("Embedded = %+v, want [{lines sales.order_line true}]", got.Embedded)
	}
	if got.PathParams["id"] != string(UUIDParam) {
		t.Errorf("PathParams[id] = %q, want %q", got.PathParams["id"], UUIDParam)
	}
}

func TestGET_NoOptions_UsesDefaults(t *testing.T) {
	withFreshRouter(t)

	GET("/orders", func(req *Request) *Response { return OK(nil) })

	got := routeDeclarations(DefaultRouter.routes)[0]
	if got.Auth != string(AuthRequired) {
		t.Errorf("Auth = %q, want %q", got.Auth, AuthRequired)
	}
	if got.TimeoutMs != 30000 {
		t.Errorf("TimeoutMs = %d, want 30000", got.TimeoutMs)
	}
	if got.MaxBodyBytes != defaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes = %d, want %d", got.MaxBodyBytes, defaultMaxBodyBytes)
	}
}

func TestWS_RegistersGETWithWebsocketFlag(t *testing.T) {
	withFreshRouter(t)

	WS("/live", func(req *Request) *Response { return OK(nil) }, Requires("sales:order:read"))

	got := routeDeclarations(DefaultRouter.routes)[0]
	if got.Method != "GET" {
		t.Errorf("Method = %q, want GET", got.Method)
	}
	if !got.Websocket {
		t.Error("Websocket = false, want true")
	}
	if len(got.Permissions) != 1 || got.Permissions[0] != "sales:order:read" {
		t.Errorf("Permissions = %+v, want [sales:order:read]", got.Permissions)
	}
}

func TestSSE_RegistersGETRoute(t *testing.T) {
	withFreshRouter(t)

	SSE("/events", func(req *Request) *Response { return OK(nil) })

	got := routeDeclarations(DefaultRouter.routes)[0]
	if got.Method != "GET" {
		t.Errorf("Method = %q, want GET", got.Method)
	}
	if got.Websocket {
		t.Error("Websocket = true, want false — SSE is not a WebSocket upgrade")
	}
}

func TestWS_DispatchesLikeAnyOtherRoute(t *testing.T) {
	withFreshRouter(t)

	WS("/live", func(req *Request) *Response { return OK("connected") })

	resp := DefaultRouter.Handle(&Request{Method: "GET", Path: "/live"})
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}
}

func TestAllVerbFunctions_ApplyOptions(t *testing.T) {
	withFreshRouter(t)

	h := func(req *Request) *Response { return OK(nil) }
	POST("/x", h, Auth(AuthNone))
	PUT("/x/{id}", h, Auth(AuthNone))
	PATCH("/x/{id}", h, Auth(AuthNone))
	DELETE("/x/{id}", h, Auth(AuthNone))

	for _, decl := range routeDeclarations(DefaultRouter.routes) {
		if decl.Auth != string(AuthNone) {
			t.Errorf("%s %s: Auth = %q, want %q", decl.Method, decl.Path, decl.Auth, AuthNone)
		}
	}
}
