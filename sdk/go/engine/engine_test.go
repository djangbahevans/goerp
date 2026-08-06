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
