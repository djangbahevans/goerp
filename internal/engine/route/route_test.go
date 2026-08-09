package route

import (
	"reflect"
	"testing"
)

func TestRouteTable_Lookup_TrailingSlashEquivalence(t *testing.T) {
	rt := New()
	entry := &RouteEntry{}
	rt.Register("GET", "/contacts", entry)

	got, _, result, _ := rt.Lookup("GET", "/contacts/")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if got != entry {
		t.Fatalf("got %+v, want %+v", got, entry)
	}
}

func TestRouteTable_Lookup_CaseSensitive(t *testing.T) {
	rt := New()
	rt.Register("GET", "/contacts", &RouteEntry{})

	_, _, result, _ := rt.Lookup("GET", "/Contacts")
	if result != RouteNotFound {
		t.Fatalf("result = %v, want RouteNotFound", result)
	}
}

func TestRouteTable_Lookup_DuplicateSlashCollapse(t *testing.T) {
	rt := New()
	entry := &RouteEntry{}
	rt.Register("GET", "/contacts", entry)

	got, _, result, _ := rt.Lookup("GET", "//contacts")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if got != entry {
		t.Fatalf("got %+v, want %+v", got, entry)
	}
}

func TestRouteTable_Lookup_RejectsDotSegment(t *testing.T) {
	rt := New()
	rt.Register("GET", "/a/{id}", &RouteEntry{})

	_, _, result, _ := rt.Lookup("GET", "/a/.")
	if result != RouteBadPath {
		t.Fatalf("result = %v, want RouteBadPath", result)
	}
}

func TestRouteTable_Lookup_RejectsDotDotSegment(t *testing.T) {
	rt := New()
	rt.Register("GET", "/a/{id}", &RouteEntry{})

	_, _, result, _ := rt.Lookup("GET", "/a/..")
	if result != RouteBadPath {
		t.Fatalf("result = %v, want RouteBadPath", result)
	}
}

func TestRouteTable_Lookup_RejectsMalformedPercentEncoding(t *testing.T) {
	rt := New()
	rt.Register("GET", "/a/{id}", &RouteEntry{})

	_, _, result, _ := rt.Lookup("GET", "/a/%zz")
	if result != RouteBadPath {
		t.Fatalf("result = %v, want RouteBadPath", result)
	}
}

func TestRouteTable_Lookup_EncodedDotDotNotRejected(t *testing.T) {
	rt := New()
	entry := &RouteEntry{}
	rt.Register("GET", "/a/{id}", entry)

	got, params, result, _ := rt.Lookup("GET", "/a/%2e%2e")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if got != entry {
		t.Fatalf("got %+v, want %+v", got, entry)
	}
	if params["id"] != ".." {
		t.Fatalf("params[id] = %q, want %q", params["id"], "..")
	}
}

func TestRouteTable_Lookup_PercentEncodedSlashNotResplit(t *testing.T) {
	rt := New()
	entry := &RouteEntry{}
	rt.Register("GET", "/files/{name}", entry)

	got, params, result, _ := rt.Lookup("GET", "/files/a%2Fb")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if got != entry {
		t.Fatalf("got %+v, want %+v", got, entry)
	}
	if params["name"] != "a/b" {
		t.Fatalf("params[name] = %q, want %q", params["name"], "a/b")
	}
}

func TestRouteTable_Lookup_StaticPrecedence_StaticRegisteredFirst(t *testing.T) {
	rt := New()
	static := &RouteEntry{}
	param := &RouteEntry{}
	rt.Register("GET", "/contacts/merge", static)
	rt.Register("GET", "/contacts/{id}", param)

	got, _, result, _ := rt.Lookup("GET", "/contacts/merge")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if got != static {
		t.Fatalf("got %+v, want the static entry %+v", got, static)
	}
}

func TestRouteTable_Lookup_StaticPrecedence_ParamRegisteredFirst(t *testing.T) {
	rt := New()
	param := &RouteEntry{}
	static := &RouteEntry{}
	rt.Register("GET", "/contacts/{id}", param)
	rt.Register("GET", "/contacts/merge", static)

	got, _, result, _ := rt.Lookup("GET", "/contacts/merge")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if got != static {
		t.Fatalf("got %+v, want the static entry %+v", got, static)
	}
}

func TestRouteTable_Lookup_ParamStillMatchesNonStaticSegment(t *testing.T) {
	rt := New()
	param := &RouteEntry{}
	static := &RouteEntry{}
	rt.Register("GET", "/contacts/{id}", param)
	rt.Register("GET", "/contacts/merge", static)

	got, params, result, _ := rt.Lookup("GET", "/contacts/123")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if got != param {
		t.Fatalf("got %+v, want the param entry %+v", got, param)
	}
	if params["id"] != "123" {
		t.Fatalf("params[id] = %q, want %q", params["id"], "123")
	}
}

func TestRouteTable_Lookup_MethodNotAllowed(t *testing.T) {
	rt := New()
	rt.Register("GET", "/contacts/{id}", &RouteEntry{})
	rt.Register("POST", "/contacts/{id}", &RouteEntry{})

	entry, params, result, allowed := rt.Lookup("DELETE", "/contacts/123")
	if result != RouteMethodNotAllowed {
		t.Fatalf("result = %v, want RouteMethodNotAllowed", result)
	}
	if entry != nil {
		t.Fatalf("entry = %+v, want nil", entry)
	}
	if params != nil {
		t.Fatalf("params = %+v, want nil", params)
	}
	want := []string{"GET", "POST"}
	if !reflect.DeepEqual(allowed, want) {
		t.Fatalf("allowed = %v, want %v", allowed, want)
	}
}

func TestRouteTable_Lookup_NotFound(t *testing.T) {
	rt := New()
	rt.Register("GET", "/contacts", &RouteEntry{})

	_, _, result, _ := rt.Lookup("GET", "/orders")
	if result != RouteNotFound {
		t.Fatalf("result = %v, want RouteNotFound", result)
	}
}

func TestRouteTable_Lookup_BareRoot(t *testing.T) {
	rt := New()
	entry := &RouteEntry{}
	rt.Register("GET", "/", entry)

	got, _, result, _ := rt.Lookup("GET", "/")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if got != entry {
		t.Fatalf("got %+v, want %+v", got, entry)
	}
}
