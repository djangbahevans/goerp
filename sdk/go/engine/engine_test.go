package engine

import "testing"

func TestAllocateDeallocateRoundTrip(t *testing.T) {
	want := []byte(`{"hello":"world"}`)

	ptr := Allocate(uint32(len(want)))
	WriteMem(ptr, want)

	got := ReadMem(ptr, uint32(len(want)))
	if string(got) != string(want) {
		t.Fatalf("ReadMem() = %q, want %q", got, want)
	}

	Deallocate(ptr, uint32(len(want)))
	if got := ReadMem(ptr, uint32(len(want))); got != nil {
		t.Fatalf("ReadMem() after Deallocate = %q, want nil", got)
	}
}

func TestDispatchRequest(t *testing.T) {
	GET("/ping", func(req *Request) *Response {
		return OK(map[string]any{"path_params": req.PathParams})
	})

	req := &Request{Method: "GET", Path: "/ping"}
	reqBytes, err := marshal(req)
	if err != nil {
		t.Fatalf("marshal(req) error: %v", err)
	}

	reqPtr := Allocate(uint32(len(reqBytes)))
	WriteMem(reqPtr, reqBytes)

	packed := DispatchRequest(reqPtr, uint32(len(reqBytes)))
	respPtr := uint32(packed >> 32)
	respLen := uint32(packed)

	respBytes := ReadMem(respPtr, respLen)

	var resp Response
	if err := unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal(resp) error: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
	}
}

// TestDispatchRequest_RawBodyRouteExposesUnparsedBytes proves
// engine.RawBody()'s documented effect end-to-end through the real
// dispatch path (DispatchRequest -> Router.Handle -> the registered
// handler): a route declared with it gets a Request whose RawBody()
// returns the exact wire bytes, unparsed — the composition goerp#245
// exists to confirm now that goerp#241 (Request.RawBody/ParseJSON) and
// goerp#243 (RouteManifest.RawBody wiring) are both closed.
func TestDispatchRequest_RawBodyRouteExposesUnparsedBytes(t *testing.T) {
	const rawPayload = "not-json-a-raw-hmac-signed-payload"
	var gotRawBody []byte

	POST("/rawbody-test", func(req *Request) *Response {
		gotRawBody = req.RawBody()
		return OK(nil)
	}, RawBody())

	req := &Request{Method: "POST", Path: "/rawbody-test", Body: []byte(rawPayload)}
	reqBytes, err := marshal(req)
	if err != nil {
		t.Fatalf("marshal(req) error: %v", err)
	}
	reqPtr := Allocate(uint32(len(reqBytes)))
	WriteMem(reqPtr, reqBytes)

	packed := DispatchRequest(reqPtr, uint32(len(reqBytes)))
	respPtr := uint32(packed >> 32)
	respLen := uint32(packed)

	var resp Response
	if err := unmarshal(ReadMem(respPtr, respLen), &resp); err != nil {
		t.Fatalf("unmarshal(resp) error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
	}
	if string(gotRawBody) != rawPayload {
		t.Errorf("req.RawBody() = %q, want %q", gotRawBody, rawPayload)
	}
}

// TestDispatchRequest_NonRawBodyRouteStillParsesJSON proves the inverse:
// a route registered without RawBody() reaches its handler with a body
// req.ParseJSON() can still successfully decode — confirming RawBody()'s
// presence or absence on one route never changes how the body reaches
// any handler, since there is no implicit JSON-parsing step in the
// dispatch path for either option to enable or disable (Router.Handle
// calls the registered handler directly; parsing only ever happens
// inside the handler, at the handler's own choice of accessor).
func TestDispatchRequest_NonRawBodyRouteStillParsesJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	var got payload

	POST("/parsejson-test", func(req *Request) *Response {
		if err := req.ParseJSON(&got); err != nil {
			return &Response{StatusCode: 400}
		}
		return OK(nil)
	})

	req := &Request{Method: "POST", Path: "/parsejson-test", Body: []byte(`{"name":"widget"}`)}
	reqBytes, err := marshal(req)
	if err != nil {
		t.Fatalf("marshal(req) error: %v", err)
	}
	reqPtr := Allocate(uint32(len(reqBytes)))
	WriteMem(reqPtr, reqBytes)

	packed := DispatchRequest(reqPtr, uint32(len(reqBytes)))
	respPtr := uint32(packed >> 32)
	respLen := uint32(packed)

	var resp Response
	if err := unmarshal(ReadMem(respPtr, respLen), &resp); err != nil {
		t.Fatalf("unmarshal(resp) error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
	}
	if got.Name != "widget" {
		t.Errorf("parsed name = %q, want %q", got.Name, "widget")
	}
}

func TestDispatchRequestNoRouteMatched(t *testing.T) {
	req := &Request{Method: "GET", Path: "/does-not-exist"}
	reqBytes, err := marshal(req)
	if err != nil {
		t.Fatalf("marshal(req) error: %v", err)
	}

	reqPtr := Allocate(uint32(len(reqBytes)))
	WriteMem(reqPtr, reqBytes)

	packed := DispatchRequest(reqPtr, uint32(len(reqBytes)))
	respPtr := uint32(packed >> 32)
	respLen := uint32(packed)

	var resp Response
	if err := unmarshal(ReadMem(respPtr, respLen), &resp); err != nil {
		t.Fatalf("unmarshal(resp) error: %v", err)
	}

	if resp.StatusCode != 404 {
		t.Fatalf("resp.StatusCode = %d, want 404", resp.StatusCode)
	}
}
