package hostcall

import (
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
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
func respondWith(t *testing.T, env abi.Envelope) Invoke {
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
	invoke := respondWith(t, abi.Envelope{OK: true, Data: respData})

	var out testResp
	if err := Do(invoke, testReq{Name: "world"}, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out.Greeting != "hi" {
		t.Errorf("Greeting = %q, want %q", out.Greeting, "hi")
	}
}

func TestDo_NilRespSkipsDecode(t *testing.T) {
	invoke := respondWith(t, abi.Envelope{OK: true})
	if err := Do(invoke, testReq{Name: "world"}, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

func TestDo_HostErrorReturnedAsGoError(t *testing.T) {
	invoke := respondWith(t, abi.Envelope{OK: false, Error: &abi.HostError{Code: abi.ErrCodeNotFound, Message: "no such record"}})

	err := Do(invoke, testReq{Name: "world"}, &testResp{})
	if err == nil {
		t.Fatal("expected an error")
	}
	hostErr, ok := err.(*abi.HostError)
	if !ok {
		t.Fatalf("error type = %T, want *abi.HostError", err)
	}
	if hostErr.Code != abi.ErrCodeNotFound {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeNotFound)
	}
	if hostErr.Error() != "orm.not_found: no such record" {
		t.Errorf("Error() = %q", hostErr.Error())
	}
}

func TestDo_NullResponsePointerIsAllocationFailed(t *testing.T) {
	invoke := func(ptr, size uint32) uint64 { return 0 }

	err := Do(invoke, testReq{Name: "world"}, &testResp{})
	hostErr, ok := err.(*abi.HostError)
	if !ok {
		t.Fatalf("error type = %T, want *abi.HostError", err)
	}
	if hostErr.Code != abi.ErrCodeAllocationFailed {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeAllocationFailed)
	}
}

func TestDo_OKFalseWithNoErrorDetail(t *testing.T) {
	invoke := respondWith(t, abi.Envelope{OK: false})

	err := Do(invoke, testReq{Name: "world"}, &testResp{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if _, ok := err.(*abi.HostError); ok {
		t.Fatal("expected a plain error, not a *abi.HostError, when the envelope carries no Error detail")
	}
}
