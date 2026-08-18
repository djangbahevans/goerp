package engine

import "testing"

func TestActionPath_ReservedNames(t *testing.T) {
	tests := []struct {
		name       ActionName
		wantMethod string
		wantPath   string
	}{
		{List, "GET", "/orders"},
		{Get, "GET", "/orders/{id}"},
		{Create, "POST", "/orders"},
		{Update, "PUT", "/orders/{id}"},
		{Delete, "DELETE", "/orders/{id}"},
		{Preview, "POST", "/orders/preview"},
	}

	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			method, path := actionPath("sales.order", tt.name)
			if method != tt.wantMethod || path != tt.wantPath {
				t.Errorf("actionPath(sales.order, %s) = (%s, %s), want (%s, %s)",
					tt.name, method, path, tt.wantMethod, tt.wantPath)
			}
		})
	}
}

func TestActionPath_CustomNameIsRecordScopedPost(t *testing.T) {
	method, path := actionPath("sales.order", "confirm")
	if method != "POST" {
		t.Errorf("method = %q, want %q", method, "POST")
	}
	if path != "/orders/{id}/confirm" {
		t.Errorf("path = %q, want %q", path, "/orders/{id}/confirm")
	}
}

func TestPluralSegment_IrregularPlural(t *testing.T) {
	// go-openapi/inflect handles irregulars a naive suffix-based pluralizer
	// wouldn't — the reason this uses that library instead of hand-rolled
	// rules.
	if got := pluralSegment("hr.person"); got != "people" {
		t.Errorf("pluralSegment(hr.person) = %q, want %q", got, "people")
	}
	if got := pluralSegment("sales.order"); got != "orders" {
		t.Errorf("pluralSegment(sales.order) = %q, want %q", got, "orders")
	}
}

func TestCrudActionOf(t *testing.T) {
	if got := crudActionOf(List); got != "list" {
		t.Errorf("crudActionOf(List) = %q, want %q", got, "list")
	}
	if got := crudActionOf("confirm"); got != "" {
		t.Errorf("crudActionOf(confirm) = %q, want empty", got)
	}
}

func TestAction_RegistersInGetRoutesPayload(t *testing.T) {
	r := NewRouter()
	prevDefault := DefaultRouter
	DefaultRouter = r
	defer func() { DefaultRouter = prevDefault }()

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
	if custom.Method != "POST" || custom.Path != "/orders/{id}/confirm" {
		t.Errorf("custom action Method/Path = %s %s, want POST /orders/{id}/confirm", custom.Method, custom.Path)
	}

	get := decls[1]
	if get.Model != "sales.order" || get.Name != "get" || get.CRUDAction != "get" {
		t.Errorf("get action declaration = %+v, want Model=sales.order Name=get CRUDAction=get", get)
	}
	if get.Method != "GET" || get.Path != "/orders/{id}" {
		t.Errorf("get action Method/Path = %s %s, want GET /orders/{id}", get.Method, get.Path)
	}
}
