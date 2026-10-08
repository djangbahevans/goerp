package engine

import (
	"reflect"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/orm"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func TestCrudActionOf(t *testing.T) {
	if got := crudActionOf(actionList); got != "list" {
		t.Errorf("crudActionOf(actionList) = %q, want %q", got, "list")
	}
	if got := crudActionOf("confirm"); got != "" {
		t.Errorf("crudActionOf(confirm) = %q, want empty", got)
	}
}

type testOrder struct{}

func (testOrder) ResourceName() string { return "sales.order" }

type testOrderLine struct{}

func (testOrderLine) ResourceName() string { return "sales.order_line" }

type confirmRequest struct {
	WarehouseID string `json:"warehouse_id"`
}

func withRouter(t *testing.T) *Router {
	t.Helper()
	r := NewRouter()
	prev := DefaultRouter
	DefaultRouter = r
	t.Cleanup(func() { DefaultRouter = prev })
	return r
}

func TestHandleAction_DeclaresIdentityWithoutMethodOrPath(t *testing.T) {
	r := withRouter(t)

	HandleAction(DefineAction[testOrder, NoBody]("confirm"), func(*Request, NoBody) *Response { return nil })
	HandleAction(Get[testOrder](), func(*Request, NoBody) *Response { return nil })

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
	HandleAction(DefineAction[testOrder, NoBody]("confirm"), func(req *Request, _ NoBody) *Response {
		gotParams = req.PathParams
		return &Response{StatusCode: 200}
	})
	HandleAction(DefineAction[testOrder, NoBody]("cancel"), func(*Request, NoBody) *Response { return &Response{StatusCode: 202} })

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
	HandleAction(DefineAction[testOrder, NoBody]("confirm"), func(*Request, NoBody) *Response { return &Response{StatusCode: 200} })
	GET("/orders", func(*Request) *Response { return &Response{StatusCode: 200} })

	resp := r.Handle(&Request{Method: "GET", Path: "/orders", Model: "sales.order", Action: "ship"})
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestRouter_HandleWithoutIdentityMatchesByPathOnly(t *testing.T) {
	r := withRouter(t)
	HandleAction(List[testOrder](), func(*Request, NoBody) *Response { return &Response{StatusCode: 201} })
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

func TestHandleAction_WithNoOptionsCarriesRouteDefaults(t *testing.T) {
	r := withRouter(t)
	HandleAction(DefineAction[testOrder, NoBody]("confirm"), func(*Request, NoBody) *Response { return nil })

	d := routeDeclarations(r.routes)[0]
	if d.Auth != string(AuthRequired) {
		t.Errorf("Auth = %q, want %q", d.Auth, AuthRequired)
	}
	if d.MaxBodyBytes != defaultMaxBodyBytes || d.TimeoutMs != int(defaultTimeout.Milliseconds()) {
		t.Errorf("MaxBodyBytes/TimeoutMs = %d/%d, want the route defaults", d.MaxBodyBytes, d.TimeoutMs)
	}
}

func TestHandleAction_OptionsAreDeclared(t *testing.T) {
	r := withRouter(t)
	HandleAction(DefineAction[testOrder, NoBody]("confirm",
		Requires(perm.Ref("sales:order:confirm")),
		RateLimit(10, 60, PerUser),
		Timeout(5*time.Second),
		MaxBody(1024),
		Embeds[testOrderLine]("lines", true),
		Method(MethodPut),
		Scope(CollectionAction),
	), func(*Request, NoBody) *Response { return nil })

	d := routeDeclarations(r.routes)[0]
	if len(d.Permissions) != 1 || d.Permissions[0] != "sales:order:confirm" {
		t.Errorf("Permissions = %v", d.Permissions)
	}
	if d.RateLimit == nil || d.RateLimit.Requests != 10 || d.RateLimit.Scope != PerUser {
		t.Errorf("RateLimit = %+v", d.RateLimit)
	}
	if d.TimeoutMs != 5000 || d.MaxBodyBytes != 1024 {
		t.Errorf("TimeoutMs/MaxBodyBytes = %d/%d, want 5000/1024", d.TimeoutMs, d.MaxBodyBytes)
	}
	if len(d.Embedded) != 1 || d.Embedded[0].Resource != "sales.order_line" {
		t.Errorf("Embedded = %+v", d.Embedded)
	}
	if d.Method != "PUT" || d.Scope != "collection" || d.Path != "" {
		t.Errorf("Method/Scope/Path = %q/%q/%q, want PUT/collection/empty", d.Method, d.Scope, d.Path)
	}
}

func TestHandleAction_ListReportsResponseIsList(t *testing.T) {
	r := withRouter(t)
	HandleAction(List[testOrder](), func(*Request, NoBody) *Response { return nil })
	HandleAction(Get[testOrder](), func(*Request, NoBody) *Response { return nil })
	HandleAction(DefineAction[testOrder, NoBody]("confirm"), func(*Request, NoBody) *Response { return nil })

	decls := routeDeclarations(r.routes)
	for i, want := range []bool{true, false, false} {
		if decls[i].ResponseIsList != want {
			t.Errorf("%s ResponseIsList = %v, want %v", decls[i].Name, decls[i].ResponseIsList, want)
		}
	}
}

func TestModel_BindsAPathRouteToAModel(t *testing.T) {
	r := withRouter(t)
	h := func(*Request) *Response { return nil }
	GET("/some/path", h, Model[testOrder](CRUDGet))
	GET("/some/list", h, Model[testOrder](CRUDList), Requires(perm.Ref("sales:order:read")))
	GET("/plain", h)

	decls := routeDeclarations(r.routes)
	if d := decls[0]; d.Model != "sales.order" || d.CRUDAction != "get" || d.ResponseIsList || d.Name != "" {
		t.Errorf("get route = %+v, want Model=sales.order CRUDAction=get ResponseIsList=false Name=\"\"", d)
	}
	if d := decls[1]; d.CRUDAction != "list" || !d.ResponseIsList || len(d.Permissions) != 1 {
		t.Errorf("list route = %+v, want CRUDAction=list ResponseIsList=true with permissions kept", d)
	}
	if d := decls[2]; d.Model != "" || d.CRUDAction != "" {
		t.Errorf("plain route = %+v, want no model binding", d)
	}
	if decls[0].Path != "/some/path" || decls[0].Method != "GET" {
		t.Errorf("path route Method/Path = %s %s, want GET /some/path", decls[0].Method, decls[0].Path)
	}
}

func TestModel_RouteStillMatchesByPath(t *testing.T) {
	r := withRouter(t)
	GET("/some/path", func(*Request) *Response { return &Response{StatusCode: 200} }, Model[testOrder](CRUDGet))

	if resp := r.Handle(&Request{Method: "GET", Path: "/some/path"}); resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHandleAction_DecodesBodyIntoReq(t *testing.T) {
	r := withRouter(t)
	var got confirmRequest
	HandleAction(DefineAction[testOrder, confirmRequest]("confirm"), func(_ *Request, body confirmRequest) *Response {
		got = body
		return &Response{StatusCode: 200}
	})

	resp := r.Handle(&Request{
		Model:  "sales.order",
		Action: "confirm",
		Body:   []byte(`{"warehouse_id":"w1"}`),
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got.WarehouseID != "w1" {
		t.Fatalf("body = %+v, want warehouse_id w1", got)
	}
}

func TestHandleAction_UndecodableBodyIs400WithoutCallingHandler(t *testing.T) {
	r := withRouter(t)
	called := false
	HandleAction(DefineAction[testOrder, confirmRequest]("confirm"), func(*Request, confirmRequest) *Response {
		called = true
		return &Response{StatusCode: 200}
	})

	for _, body := range []string{`{"warehouse_id":`, `{"warehouse_id":7}`, ``} {
		resp := r.Handle(&Request{Model: "sales.order", Action: "confirm", Body: []byte(body)})
		if resp.StatusCode != 400 {
			t.Errorf("body %q: status = %d, want 400", body, resp.StatusCode)
		}
	}
	if called {
		t.Fatal("handler was called for an undecodable body")
	}
}

func TestHandleAction_NoBodyDoesNotDecode(t *testing.T) {
	r := withRouter(t)
	HandleAction(DefineAction[testOrder, NoBody]("archive"), func(*Request, NoBody) *Response {
		return &Response{StatusCode: 204}
	})

	resp := r.Handle(&Request{Model: "sales.order", Action: "archive", Body: []byte(`not json`)})
	if resp.StatusCode != 204 {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if d := routeDeclarations(r.routes)[0]; d.RequestType != nil {
		t.Errorf("RequestType = %+v, want nil for NoBody", d.RequestType)
	}
}

func TestHandleAction_ReqTypeIsTheDeclaredRequestType(t *testing.T) {
	r := withRouter(t)
	HandleAction(DefineAction[testOrder, confirmRequest]("confirm"), func(*Request, confirmRequest) *Response { return nil })

	d := routeDeclarations(r.routes)[0]
	if d.RequestType == nil || !reflect.DeepEqual(*d.RequestType, describeType(reflect.TypeFor[confirmRequest]())) {
		t.Fatalf("RequestType = %+v, want confirmRequest described", d.RequestType)
	}
	if d.Model != "sales.order" || d.Name != "confirm" {
		t.Errorf("identity = %q/%q, want sales.order/confirm", d.Model, d.Name)
	}
}

func TestHandleAction_NilHandlerPanics(t *testing.T) {
	withRouter(t)
	defer func() {
		if recover() == nil {
			t.Fatal("HandleAction with a nil handler did not panic")
		}
	}()
	HandleAction(DefineAction[testOrder, NoBody]("confirm"), nil)
}

type testContact struct{}

func (testContact) ResourceName() string { return "contacts.contact" }

func TestReservedConstructors_DeclareTheirOwnNameAndBody(t *testing.T) {
	r := withRouter(t)
	h := func(*Request, NoBody) *Response { return nil }
	hv := func(*Request, *orm.Values[testOrder]) *Response { return nil }
	HandleAction(List[testOrder](), h)
	HandleAction(Get[testOrder](), h)
	HandleAction(Delete[testOrder](), h)
	HandleAction(Pivot[testOrder](), h)
	HandleAction(Create[testOrder](), hv)
	HandleAction(Update[testOrder](), hv)
	HandleAction(Preview[testOrder](), hv)

	decls := routeDeclarations(r.routes)
	wantNames := []string{"list", "get", "delete", "pivot", "create", "update", "preview"}
	for i, want := range wantNames {
		d := decls[i]
		if d.Model != "sales.order" || d.Name != want || d.CRUDAction != want {
			t.Errorf("decls[%d] = %s/%s/%s, want sales.order/%s/%s", i, d.Model, d.Name, d.CRUDAction, want, want)
		}
		hasBody := i >= 4
		if (d.RequestType != nil) != hasBody {
			t.Errorf("%s RequestType = %+v, want body declared = %v", want, d.RequestType, hasBody)
		}
	}
	if !decls[0].ResponseIsList || decls[1].ResponseIsList {
		t.Errorf("ResponseIsList list/get = %v/%v, want true/false", decls[0].ResponseIsList, decls[1].ResponseIsList)
	}
}

func TestReservedConstructors_TakeActionOptions(t *testing.T) {
	r := withRouter(t)
	HandleAction(Get[testOrder](Requires(perm.Ref("sales:order:read")), Timeout(5*time.Second)),
		func(*Request, NoBody) *Response { return nil })

	d := routeDeclarations(r.routes)[0]
	if len(d.Permissions) != 1 || d.Permissions[0] != "sales:order:read" || d.TimeoutMs != 5000 {
		t.Errorf("declaration = %+v, want the options applied", d)
	}
}

type testWritableOrder struct{}

func (testWritableOrder) ResourceName() string { return "sales.writable_order" }

func (testWritableOrder) ValueFields() map[string]orm.ValueDecoder {
	return map[string]orm.ValueDecoder{
		"id":       nil,
		"name":     orm.DecodeValue[string],
		"quantity": orm.DecodeValue[int32],
	}
}

func TestReservedValuesActions_DecodeBodyBeforeTheHandler(t *testing.T) {
	for _, tt := range []struct {
		name string
		def  func() ActionDef[testWritableOrder, *orm.Values[testWritableOrder]]
	}{
		{"create", func() ActionDef[testWritableOrder, *orm.Values[testWritableOrder]] {
			return Create[testWritableOrder]()
		}},
		{"update", func() ActionDef[testWritableOrder, *orm.Values[testWritableOrder]] {
			return Update[testWritableOrder]()
		}},
		{"preview", func() ActionDef[testWritableOrder, *orm.Values[testWritableOrder]] {
			return Preview[testWritableOrder]()
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := withRouter(t)
			var got *orm.Values[testWritableOrder]
			calls := 0
			HandleAction(tt.def(), func(_ *Request, body *orm.Values[testWritableOrder]) *Response {
				calls++
				got = body
				return &Response{StatusCode: 201}
			})
			handle := func(body string) *Response {
				return r.Handle(&Request{Model: "sales.writable_order", Action: tt.name, Body: []byte(body)})
			}

			if resp := handle(`{"name":"x","quantity":2}`); resp.StatusCode != 201 || got == nil {
				t.Fatalf("valid body: status = %d, values = %v, want 201 and a Values", resp.StatusCode, got)
			}

			calls = 0
			for _, tc := range []struct{ body, field string }{
				{`{"nope":1}`, "nope"},
				{`{"id":"1"}`, "id"},
				{`{"quantity":"2"}`, "quantity"},
			} {
				resp := handle(tc.body)
				if resp.StatusCode != 422 {
					t.Errorf("%s: status = %d, want 422", tc.body, resp.StatusCode)
					continue
				}
				errBody := resp.Body.(map[string]any)["error"].(map[string]any)
				if errBody["code"] != "orm.validation_failed" || errBody["details"].(map[string]any)["field"] != tc.field {
					t.Errorf("%s: error = %v, want orm.validation_failed naming %q", tc.body, errBody, tc.field)
				}
			}
			if calls != 0 {
				t.Errorf("handler called %d times for rejected bodies, want 0", calls)
			}

			for _, body := range []string{`[1]`, `{"name":`} {
				if resp := handle(body); resp.StatusCode != 400 {
					t.Errorf("%s: status = %d, want 400", body, resp.StatusCode)
				}
			}
		})
	}
}

func TestModel_ResponseIsListOnlyForCRUDList(t *testing.T) {
	c := newRouteConfig(Model[testOrder](CRUDList))
	if c.model != "sales.order" || c.crudAction != "list" || !c.responseIsList {
		t.Errorf("list config = %+v", c)
	}
	c = newRouteConfig(Model[testOrder](CRUDPreview))
	if c.crudAction != "preview" || c.responseIsList {
		t.Errorf("preview config = %+v", c)
	}
}
