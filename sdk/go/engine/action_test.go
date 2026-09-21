package engine

import "testing"

func TestCrudActionOf(t *testing.T) {
	if got := crudActionOf(List); got != "list" {
		t.Errorf("crudActionOf(List) = %q, want %q", got, "list")
	}
	if got := crudActionOf("confirm"); got != "" {
		t.Errorf("crudActionOf(confirm) = %q, want empty", got)
	}
}

func withRouter(t *testing.T) *Router {
	t.Helper()
	r := NewRouter()
	prev := DefaultRouter
	DefaultRouter = r
	t.Cleanup(func() { DefaultRouter = prev })
	return r
}

func TestAction_DeclaresIdentityWithoutMethodOrPath(t *testing.T) {
	r := withRouter(t)

	Action("sales.order", "confirm", func(*Request) *Response { return nil })
	Action("sales.order", Get, func(*Request) *Response { return nil })

	decls := routeDeclarations(r.routes)
	if len(decls) != 2 {
		t.Fatalf("got %d route declarations, want 2", len(decls))
	}

	custom := decls[0]
	if custom.Model != "sales.order" || custom.Name != "confirm" || custom.CRUDAction != "" {
		t.Errorf("custom action declaration = %+v, want Model=sales.order Name=confirm CRUDAction=\"\"", custom)
	}
	get := decls[1]
	if get.Model != "sales.order" || get.Name != "get" || get.CRUDAction != "get" {
		t.Errorf("get action declaration = %+v, want Model=sales.order Name=get CRUDAction=get", get)
	}
	for _, d := range decls {
		if d.Method != "" || d.Path != "" {
			t.Errorf("action %s declared Method/Path = %q %q, want both empty", d.Name, d.Method, d.Path)
		}
	}
}

func TestRouter_HandleDispatchesActionByIdentity(t *testing.T) {
	r := withRouter(t)
	var gotParams map[string]string
	Action("sales.order", "confirm", func(req *Request) *Response {
		gotParams = req.PathParams
		return &Response{StatusCode: 200}
	})
	Action("sales.order", "cancel", func(*Request) *Response { return &Response{StatusCode: 202} })

	resp := r.Handle(&Request{
		Method:     "POST",
		Path:       "/sales-orders/abc/confirm",
		Model:      "sales.order",
		Action:     "confirm",
		PathParams: map[string]string{"id": "abc"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gotParams["id"] != "abc" {
		t.Fatalf("PathParams = %v, want the engine-supplied id=abc", gotParams)
	}

	resp = r.Handle(&Request{Model: "sales.order", Action: "cancel"})
	if resp.StatusCode != 202 {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
}

func TestRouter_HandleUnknownActionIdentityIsNotFound(t *testing.T) {
	r := withRouter(t)
	Action("sales.order", "confirm", func(*Request) *Response { return &Response{StatusCode: 200} })
	GET("/orders", func(*Request) *Response { return &Response{StatusCode: 200} })

	resp := r.Handle(&Request{Method: "GET", Path: "/orders", Model: "sales.order", Action: "ship"})
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestRouter_HandleWithoutIdentityMatchesByPathOnly(t *testing.T) {
	r := withRouter(t)
	Action("sales.order", List, func(*Request) *Response { return &Response{StatusCode: 201} })
	GET("/", func(*Request) *Response { return &Response{StatusCode: 200} })
	GET("/hooks/{name}", func(req *Request) *Response {
		if req.PathParams["name"] != "stripe" {
			t.Errorf("PathParams = %v, want name=stripe", req.PathParams)
		}
		return &Response{StatusCode: 204}
	})

	if resp := r.Handle(&Request{Method: "GET", Path: "/hooks/stripe"}); resp.StatusCode != 204 {
		t.Fatalf("path route status = %d, want 204", resp.StatusCode)
	}
	if resp := r.Handle(&Request{Method: "GET", Path: "/"}); resp.StatusCode != 200 {
		t.Fatalf("root path route status = %d, want 200 — an action route must not shadow it", resp.StatusCode)
	}
	if resp := r.Handle(&Request{Method: "GET", Path: "/orders"}); resp.StatusCode != 404 {
		t.Fatalf("action route reached by path: status = %d, want 404", resp.StatusCode)
	}
}
