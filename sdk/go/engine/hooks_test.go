package engine

import (
	"errors"
	"strings"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

func TestDispatchHook(t *testing.T) {
	old := hookHandlers
	hookHandlers = make(map[string]HookHandler)
	t.Cleanup(func() { hookHandlers = old })
	calls := 0
	HandleHook("test", func(in HookRequest) (HookResult, error) {
		calls++
		if in.Request.UserID == "panic" {
			panic("boom")
		}
		if in.Request.UserID == "error" {
			return HookResult{}, errors.New("rejected")
		}
		req := in.Request
		req.Body = []byte("changed")
		return HookResult{Request: &req}, nil
	})

	for _, tc := range []struct {
		name  string
		req   HookRequest
		want  string
		count int
	}{
		{name: "validate", req: HookRequest{Hook: abi.HookRequestBefore, Handler: "test", Validate: true}, count: 0},
		{name: "transform", req: HookRequest{Hook: abi.HookRequestBefore, Handler: "test"}, count: 1},
		{name: "unknown handler", req: HookRequest{Hook: abi.HookRequestBefore, Handler: "missing"}, want: "not registered", count: 1},
		{name: "unknown hook", req: HookRequest{Hook: "engine.request.typo", Handler: "test"}, want: "unsupported hook", count: 1},
		{name: "error", req: HookRequest{Hook: abi.HookRequestBefore, Handler: "test", Request: abi.Request{UserID: "error"}}, want: "rejected", count: 2},
		{name: "panic", req: HookRequest{Hook: abi.HookRequestBefore, Handler: "test", Request: abi.Request{UserID: "panic"}}, want: "panicked", count: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := msgpack.Marshal(tc.req)
			if err != nil {
				t.Fatal(err)
			}
			ptr := Allocate(uint32(len(data)))
			WriteMem(ptr, data)
			defer Deallocate(ptr, uint32(len(data)))

			packed := DispatchHook(ptr, uint32(len(data)))
			responsePtr, length := uint32(packed>>32), uint32(packed)
			defer Deallocate(responsePtr, length)
			var result HookResult
			if err := msgpack.Unmarshal(ReadMem(responsePtr, length), &result); err != nil {
				t.Fatal(err)
			}

			if (tc.want == "" && result.Error != "") || (tc.want != "" && !strings.Contains(result.Error, tc.want)) {
				t.Fatalf("error = %q, want %q", result.Error, tc.want)
			}
			if calls != tc.count {
				t.Fatalf("calls = %d, want %d", calls, tc.count)
			}
			if tc.name == "transform" && (result.Request == nil || string(result.Request.Body) != "changed") {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestHandleHookInvalidRegistration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler HookHandler
	}{
		{name: ""},
		{name: "nil"},
		{name: "duplicate", handler: func(HookRequest) (HookResult, error) { return HookResult{}, nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := hookHandlers
			hookHandlers = map[string]HookHandler{"duplicate": tc.handler}
			t.Cleanup(func() { hookHandlers = old })
			defer func() {
				if recover() == nil {
					t.Fatal("expected registration panic")
				}
			}()
			HandleHook(tc.name, tc.handler)
		})
	}
}
