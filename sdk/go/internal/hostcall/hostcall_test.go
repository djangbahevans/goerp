package hostcall

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/internal/wasmmem"
	"github.com/vmihailenco/msgpack/v5"
)

type testReq struct {
	Name string `msgpack:"name"`
}

type testResp struct {
	Greeting string `msgpack:"greeting"`
}

// respondWith builds an Invoke that ignores the request bytes and always
// returns env, packed the same way a real host function's response would
// be — proving Do's own unpack/decode logic without needing a real WASM
// host to call.
func respondWith(t *testing.T, env envelope) Invoke {
	t.Helper()
	data, err := msgpack.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return func(ptr, size uint32) uint64 {
		respPtr := wasmmem.Allocate(uint32(len(data)))
		wasmmem.WriteMem(respPtr, data)
		return uint64(respPtr)<<32 | uint64(len(data))
	}
}

func TestDo_SuccessDecodesResponse(t *testing.T) {
	respData, err := msgpack.Marshal(testResp{Greeting: "hi"})
	if err != nil {
		t.Fatalf("marshal resp: %v", err)
	}
	invoke := respondWith(t, envelope{OK: true, Data: respData})

	var out testResp
	if err := Do(invoke, testReq{Name: "world"}, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out.Greeting != "hi" {
		t.Errorf("Greeting = %q, want %q", out.Greeting, "hi")
	}
}

func TestDo_NilRespSkipsDecode(t *testing.T) {
	invoke := respondWith(t, envelope{OK: true})
	if err := Do(invoke, testReq{Name: "world"}, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

func TestDo_HostErrorReturnedAsGoError(t *testing.T) {
	invoke := respondWith(t, envelope{OK: false, Error: &HostError{Code: "orm.not_found", Message: "no such record"}})

	err := Do(invoke, testReq{Name: "world"}, &testResp{})
	if err == nil {
		t.Fatal("expected an error")
	}
	hostErr, ok := err.(*HostError)
	if !ok {
		t.Fatalf("error type = %T, want *HostError", err)
	}
	if hostErr.Code != "orm.not_found" {
		t.Errorf("Code = %q, want %q", hostErr.Code, "orm.not_found")
	}
	if hostErr.Error() != "orm.not_found: no such record" {
		t.Errorf("Error() = %q", hostErr.Error())
	}
}

func TestDo_NullResponsePointerIsAllocationFailed(t *testing.T) {
	invoke := func(ptr, size uint32) uint64 { return 0 }

	err := Do(invoke, testReq{Name: "world"}, &testResp{})
	hostErr, ok := err.(*HostError)
	if !ok {
		t.Fatalf("error type = %T, want *HostError", err)
	}
	if hostErr.Code != "abi.allocation_failed" {
		t.Errorf("Code = %q, want %q", hostErr.Code, "abi.allocation_failed")
	}
}

func TestDo_OKFalseWithNoErrorDetail(t *testing.T) {
	invoke := respondWith(t, envelope{OK: false})

	err := Do(invoke, testReq{Name: "world"}, &testResp{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if _, ok := err.(*HostError); ok {
		t.Fatal("expected a plain error, not a *HostError, when the envelope carries no Error detail")
	}
}
