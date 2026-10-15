package wasm

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/vmihailenco/msgpack/v5"
)

type uiPushRecorder struct {
	channel, tenant, msgType string
	payload                  any
	calls                    int
	err                      error
}

func (p *uiPushRecorder) BroadcastCounted(_ context.Context, channel, tenant, msgType string, payload any) (int, error) {
	p.channel, p.tenant, p.msgType, p.payload = channel, tenant, msgType, payload
	p.calls++
	return 3, p.err
}

func TestUIPushHostValidationAndCapability(t *testing.T) {
	rt := newTestRuntime(t, 8<<20)
	pusher := &uiPushRecorder{}
	rt.SetUIPusher(pusher)
	mc := newTestModuleContext("acme", abi.CapUIPush, rt.TxLimiter())
	inst := newHostCallerInstance(t, t.Context(), rt, buildHostCallerModule("host.ui", []string{"push"}), mc)
	if _, ok := inst.module.Memory().Grow(32); !ok {
		t.Fatal("grow test memory")
	}
	encode := func(value any) []byte {
		t.Helper()
		raw, err := msgpack.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	valid := encode([]map[string]any{
		{"type": "toast", "message": "Done", "severity": "success", "duration_ms": 0},
		{"type": "reload", "resource": "sales.invoice", "record_id": "inv1"},
		{"type": "set_badge", "target": "/sales/count", "count": 0},
	})
	for _, channel := range []string{"ui:user:u1", "ui:role:admin", "ui:record:sales.invoice:inv1"} {
		out := callHost(t, t.Context(), inst, "call_push", abiv1.UIPushInput{Channel: channel, Instructions: valid})
		if !out.OK {
			t.Fatal(out.Error)
		}
		var result abiv1.UIPushOutput
		if err := msgpack.Unmarshal(out.Data, &result); err != nil || result.DeliveredCount != 3 {
			t.Fatalf("output = %+v, %v", result, err)
		}
		if pusher.channel != channel || pusher.tenant != mc.TenantID || pusher.msgType != "ui.push" {
			t.Fatalf("push = %+v", pusher)
		}
	}

	for _, channel := range []string{"", "notifications", "ui:user:", "ui:role:", "ui:record::1", "ui:record:sales.invoice:", "ui:user:u1:extra", "ui:record:sales.invoice:1:extra", "record:sales.invoice:1", "ui:tenant:t1"} {
		before := pusher.calls
		out := callHost(t, t.Context(), inst, "call_push", abiv1.UIPushInput{Channel: channel, Instructions: valid})
		if out.OK || out.Error.Code != abiv1.ErrCodeUIChannelInvalid || pusher.calls != before {
			t.Fatalf("channel %q = %+v; calls %d -> %d", channel, out, before, pusher.calls)
		}
	}
	for _, kind := range []string{"navigate", "open_form", "open_dialog", "close_modal", "open_url", "unknown", ""} {
		before := pusher.calls
		out := callHost(t, t.Context(), inst, "call_push", abiv1.UIPushInput{
			Channel: "ui:user:u1", Instructions: encode([]map[string]any{{"type": "toast"}, {"type": kind}}),
		})
		if out.OK || out.Error.Code != abiv1.ErrCodeUIInstructionNotPushable || pusher.calls != before {
			t.Fatalf("type %q = %+v; delivered invalid batch", kind, out)
		}
	}

	base := encode([]map[string]any{{"type": "toast", "message": strings.Repeat("x", 1000)}})
	atLimit := encode([]map[string]any{{"type": "toast", "message": strings.Repeat("x", 1000+(64<<10)-len(base))}})
	if len(atLimit) != 64<<10 {
		t.Fatalf("boundary payload = %d bytes", len(atLimit))
	}
	for _, input := range []struct {
		raw  []byte
		code string
	}{
		{atLimit, ""},
		{append(atLimit, 0), abiv1.ErrCodeUIPayloadTooLarge},
		{[]byte{0xc1}, abiv1.ErrCodeDeserializeError},
		{encode(nil), abiv1.ErrCodeDeserializeError},
		{encode(map[string]any{"type": "reload"}), abiv1.ErrCodeDeserializeError},
		{encode([]map[string]any{{"type": "reload"}, nil}), abiv1.ErrCodeUIInstructionNotPushable},
		{encode([]map[string]any{{"type": "set_badge", "count": math.NaN()}}), abiv1.ErrCodeDeserializeError},
	} {
		before := pusher.calls
		out := callHost(t, t.Context(), inst, "call_push", abiv1.UIPushInput{Channel: "ui:user:u1", Instructions: input.raw})
		if input.code == "" {
			if !out.OK {
				t.Fatal(out.Error)
			}
		} else if out.OK || out.Error.Code != input.code || pusher.calls != before {
			t.Fatalf("payload error = %+v, want %s; calls %d -> %d", out, input.code, before, pusher.calls)
		}
	}

	inst.SetModuleContext(newTestModuleContext("acme", 0, rt.TxLimiter()))
	out := callHost(t, t.Context(), inst, "call_push", abiv1.UIPushInput{Channel: "invalid"})
	if out.OK || out.Error.Code != abiv1.ErrCodeCapabilityDenied {
		t.Fatalf("capability error = %+v", out)
	}
	inst.SetModuleContext(mc)
	pusher.err = errors.New("transport unavailable")
	out = callHost(t, t.Context(), inst, "call_push", abiv1.UIPushInput{Channel: "ui:user:u1", Instructions: valid})
	if out.OK || out.Error.Code != abiv1.ErrCodeUnavailable || out.Error.Retry {
		t.Fatalf("delivery error = %+v", out)
	}
}
