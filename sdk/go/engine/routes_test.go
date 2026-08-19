package engine

import (
	"reflect"
	"testing"
)

func TestRouteDeclaration_MsgpackRoundTrip(t *testing.T) {
	original := RouteDeclaration{
		Method:      "POST",
		Path:        "/contacts/{id}/confirm",
		Auth:        "required",
		Permissions: []string{"contacts:contact:write"},
		RateLimit: &RateLimitDecl{
			Requests:      100,
			WindowSeconds: 60,
			Scope:         PerUser,
		},
		MaxBodyBytes:   65536,
		TimeoutMs:      30000,
		Streaming:      false,
		Websocket:      false,
		RawBody:        true,
		Model:          "contacts.contact",
		Name:           "confirm",
		CRUDAction:     "update",
		ResponseIsList: false,
		Embedded: []EmbeddedDecl{
			{Field: "lines", Resource: "sales.order_line", IsList: true},
		},
		PathParams: map[string]string{"id": "uuid"},
	}

	data, err := marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded RouteDeclaration
	if err := unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round-trip mismatch:\n got:  %+v\n want: %+v", decoded, original)
	}
	if decoded.RateLimit == nil || *decoded.RateLimit != *original.RateLimit {
		t.Fatalf("RateLimit round-trip mismatch: got %+v, want %+v", decoded.RateLimit, original.RateLimit)
	}
}

func TestRouteDeclaration_OmitemptyVsAlwaysPresent(t *testing.T) {
	// Zero-value RouteDeclaration: RateLimit/Model/Name/CRUDAction/Embedded/
	// PathParams are all omitempty-tagged and should be absent from the wire
	// bytes. MaxBodyBytes has no omitempty — 0 is a meaningful, explicitly
	// declared value (engine.MaxBody's own "Use 0 for no body"), not merely
	// "unset" — so it must stay present even at its zero value.
	data, err := marshal(RouteDeclaration{Method: "GET", Path: "/contacts"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var asMap map[string]any
	if err := unmarshal(data, &asMap); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}

	for _, key := range []string{"rate_limit", "model", "name", "crud_action", "embedded", "path_params"} {
		if _, present := asMap[key]; present {
			t.Errorf("key %q present in wire bytes at zero value, want omitted", key)
		}
	}
	for _, key := range []string{"max_body_bytes", "timeout_ms", "streaming", "websocket", "raw_body", "response_is_list"} {
		if _, present := asMap[key]; !present {
			t.Errorf("key %q absent from wire bytes at zero value, want always present", key)
		}
	}
}

func TestRouteDeclarations_BasicPath(t *testing.T) {
	decls := routeDeclarations([]route{
		{method: "GET", segments: []string{"orders"}},
	})

	if len(decls) != 1 {
		t.Fatalf("got %d decls, want 1", len(decls))
	}
	if decls[0].Method != "GET" || decls[0].Path != "/orders" {
		t.Fatalf("got %+v, want Method=GET Path=/orders", decls[0])
	}
}

func TestRouteDeclarations_RootPath(t *testing.T) {
	// register() builds segments via strings.Split(strings.Trim(pattern, "/"), "/"),
	// which for the root pattern produces []string{""} — a one-element slice
	// holding an empty string, not an empty slice. Reconstructing via
	// "/" + strings.Join(segments, "/") must still come back to "/".
	decls := routeDeclarations([]route{
		{method: "GET", segments: []string{""}},
	})

	if len(decls) != 1 {
		t.Fatalf("got %d decls, want 1", len(decls))
	}
	if decls[0].Path != "/" {
		t.Fatalf("Path = %q, want \"/\"", decls[0].Path)
	}
}

func TestRouteDeclarations_MultiSegmentWithParam(t *testing.T) {
	decls := routeDeclarations([]route{
		{method: "PUT", segments: []string{"contacts", "{id}"}},
	})

	if decls[0].Path != "/contacts/{id}" {
		t.Fatalf("Path = %q, want \"/contacts/{id}\"", decls[0].Path)
	}
}

func TestRouteDeclarations_RestFieldsAtZeroValue(t *testing.T) {
	// routeDeclarations is a pure field-copy — a route value built by hand,
	// bypassing register()'s default-seeding in newRouteConfig, must come
	// back at zero value rather than having defaults fabricated here.
	decls := routeDeclarations([]route{
		{method: "GET", segments: []string{"orders"}},
	})

	got := decls[0]
	want := RouteDeclaration{Method: "GET", Path: "/orders"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v (only Method/Path set)", got, want)
	}
}

func TestRouteDeclarations_PreservesCountAndOrder(t *testing.T) {
	decls := routeDeclarations([]route{
		{method: "GET", segments: []string{"a"}},
		{method: "POST", segments: []string{"b"}},
		{method: "DELETE", segments: []string{"c"}},
	})

	if len(decls) != 3 {
		t.Fatalf("got %d decls, want 3", len(decls))
	}
	for i, want := range []string{"a", "b", "c"} {
		if decls[i].Path != "/"+want {
			t.Fatalf("decls[%d].Path = %q, want %q", i, decls[i].Path, "/"+want)
		}
	}
}

func TestRouteDeclarations_EmptyInput(t *testing.T) {
	decls := routeDeclarations(nil)
	if len(decls) != 0 {
		t.Fatalf("got %d decls, want 0", len(decls))
	}
}

func TestWriteRoutes_PackingConvention(t *testing.T) {
	want := []RouteDeclaration{
		{Method: "GET", Path: "/contacts"},
		{Method: "GET", Path: "/contacts/{id}"},
	}

	packed := WriteRoutes(want)
	ptr := uint32(packed >> 32)
	length := uint32(packed)

	data := ReadMem(ptr, length)

	var got []RouteDeclaration
	if err := unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSerialiseRouteTable(t *testing.T) {
	orig := DefaultRouter
	DefaultRouter = NewRouter()
	defer func() { DefaultRouter = orig }()

	GET("/contacts", func(req *Request) *Response { return OK(nil) })
	GET("/contacts/{id}", func(req *Request) *Response { return OK(nil) })
	POST("/contacts", func(req *Request) *Response { return OK(nil) })

	packed := SerialiseRouteTable()
	ptr := uint32(packed >> 32)
	length := uint32(packed)

	var got []RouteDeclaration
	if err := unmarshal(ReadMem(ptr, length), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("got %d routes, want 3", len(got))
	}

	want := map[string]string{
		"/contacts":      "GET",
		"/contacts/{id}": "GET",
	}
	seenPostContacts := false
	for _, d := range got {
		if d.Method == "POST" && d.Path == "/contacts" {
			seenPostContacts = true
			continue
		}
		if method, ok := want[d.Path]; !ok || method != d.Method {
			t.Errorf("unexpected route %+v", d)
		}
	}
	if !seenPostContacts {
		t.Error("missing POST /contacts")
	}
}
