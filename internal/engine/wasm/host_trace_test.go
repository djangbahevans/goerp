package wasm

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/vmihailenco/msgpack/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func compileTraceFixture(t *testing.T) []byte {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tracefixture.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", path, "./testdata/tracefixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile trace fixture: %v\n%s", err, output)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	wasmtest.Precompile(t, data, wasmtest.SharedCompilationCacheDir())

	return data
}

func newTraceFixture(t *testing.T, binary []byte, tracer trace.Tracer) (*Runtime, *ModuleInstance) {
	t.Helper()

	r := newHostcallTestRuntime(t, nil, 10)
	r.tracer = tracer
	compiled, err := r.CompileModule(t.Context(), binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	inst, err := newModuleInstance(t.Context(), "tracefixture", compiled, r)
	if err != nil {
		t.Fatal(err)
	}
	inst.SetModuleContext(NewModuleContext("request-1", "tracefixture", "", "", nil, nil, "tenant-1", "trace-test", "", 0, nil, ModuleSnapshot{}))
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })
	return r, inst
}

func testTraceRecorder(t *testing.T) (trace.Tracer, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	return provider.Tracer("test"), recorder
}

func spansByName(t *testing.T, recorder *tracetest.SpanRecorder) map[string]sdktrace.ReadOnlySpan {
	t.Helper()

	spans := make(map[string]sdktrace.ReadOnlySpan)
	for _, span := range recorder.Ended() {
		spans[span.Name()] = span
	}
	return spans
}

func traceFixtureCodes(t *testing.T, data []byte) []string {
	t.Helper()

	var result struct {
		Codes []string `msgpack:"codes"`
	}
	if err := msgpack.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result.Codes
}

func TestHostTraceFixture(t *testing.T) {
	binary := compileTraceFixture(t)
	tracer, recorder := testTraceRecorder(t)
	_, inst := newTraceFixture(t, binary, tracer)
	ctx, root := tracer.Start(t.Context(), "request")

	data, err := inst.InvokeNoArg(ctx, "run_flow")
	if err != nil {
		t.Fatal(err)
	}
	gotCodes := traceFixtureCodes(t, data)
	wantCodes := []string{"", "", abiv1.ErrCodeTraceSpanNotFound, abiv1.ErrCodeTraceSpanNotFound, "", abiv1.ErrCodeTraceSpanNotFound, abiv1.ErrCodeTraceSpanNotFound}
	for i, want := range wantCodes {
		if len(gotCodes) <= i || gotCodes[i] != want {
			t.Fatalf("codes = %v, want prefix %v", gotCodes, wantCodes)
		}
	}
	if len(gotCodes) != 8 || !strings.HasPrefix(gotCodes[7], abiv1.ErrCodeCapabilityDenied) {
		t.Fatalf("cache denial = %v", gotCodes)
	}

	data, err = inst.InvokeNoArg(ctx, "run_stale")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range traceFixtureCodes(t, data) {
		if code != abiv1.ErrCodeTraceSpanNotFound {
			t.Fatalf("stale span accepted: %s", code)
		}
	}
	root.End()

	spans := spansByName(t, recorder)
	for child, parent := range map[string]string{
		"business.outer":          "request",
		"business.inner":          "business.outer",
		"host.crypto.verify_hmac": "business.inner",
		"host.cache.get":          "business.inner",
		"raw":                     "business.outer",
		"leaked":                  "business.outer",
	} {
		c, p := spans[child], spans[parent]
		if c == nil || p == nil || c.Parent().SpanID() != p.SpanContext().SpanID() {
			t.Errorf("%s is not a child of %s", child, parent)
		}
	}
	for _, name := range []string{"host.trace.span_start", "host.trace.span_attr", "host.trace.span_end"} {
		if spans[name] == nil {
			t.Errorf("missing automatic span %s", name)
		}
	}
	if len(recorder.Started()) != len(recorder.Ended()) {
		t.Fatalf("unclosed spans: started %d, ended %d", len(recorder.Started()), len(recorder.Ended()))
	}
	for _, span := range recorder.Ended() {
		if span.Name() == "host.trace.span_start" && span.Parent().SpanID() == spans["business.inner"].SpanContext().SpanID() {
			t.Error("span_start adopted the span it created as its own parent")
		}
	}
	inner := spans["business.inner"]
	if inner.Status().Code != codes.Error || inner.Status().Description != "invalid total" {
		t.Errorf("inner status = %v", inner.Status())
	}
	if events := inner.Events(); len(events) != 1 || events[0].Name != "exception" {
		t.Errorf("inner exceptions = %v", events)
	}
	if spans["business.outer"].Status().Code != codes.Ok || spans["host.cache.get"].Status().Code != codes.Error {
		t.Error("success and host-error statuses were not recorded")
	}

	attrs := attribute.NewSet(spans["business.outer"].Attributes()...)
	for key, want := range map[attribute.Key]attribute.Value{
		"order.id":     attribute.StringValue("order-1"),
		"order.total":  attribute.Int64Value(9007199254740993),
		"order.rate":   attribute.Float64Value(1.5),
		"order.active": attribute.BoolValue(true),
		"outer.result": attribute.StringValue("failed"),
	} {
		if got, ok := attrs.Value(key); !ok || got != want {
			t.Errorf("attribute %s = %v, want %v", key, got, want)
		}
	}
}

func TestHostTraceInvocationCleanup(t *testing.T) {
	binary := compileTraceFixture(t)
	for _, mode := range []string{"request", "compute", "event", "job", "cron", "panic", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			tracer, recorder := testTraceRecorder(t)
			_, inst := newTraceFixture(t, binary, tracer)
			ctx := t.Context()
			var err error
			switch mode {
			case "request":
				_, err = inst.InvokeHandleRequest(ctx, []byte("normal"))
			case "compute":
				_, err = inst.InvokeHandleComputed(ctx, []byte("normal"))
			case "event":
				_, err = inst.InvokeHandleEvent(ctx, []byte("normal"))
			case "job":
				_, err = inst.InvokeHandleJob(ctx, []byte("normal"))
			case "cron":
				_, err = inst.InvokeHandleCron(ctx, []byte("normal"))
			case "panic":
				_, err = inst.InvokeHandleRequest(ctx, []byte("panic"))
			case "timeout":
				deadlineCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
				_, err = inst.InvokeHandleRequest(deadlineCtx, []byte("timeout"))
			}
			if mode == "panic" || mode == "timeout" {
				if err == nil {
					t.Fatal("expected invocation failure")
				}
			} else if err != nil {
				t.Fatal(err)
			}

			if len(recorder.Started()) == 0 || len(recorder.Started()) != len(recorder.Ended()) {
				t.Fatalf("started %d, ended %d", len(recorder.Started()), len(recorder.Ended()))
			}
		})
	}
}

func TestHostTraceNoExporter(t *testing.T) {
	_, inst := newTraceFixture(t, compileTraceFixture(t), noop.NewTracerProvider().Tracer("test"))
	data, err := inst.InvokeNoArg(t.Context(), "run_flow")
	if err != nil {
		t.Fatal(err)
	}
	gotCodes := traceFixtureCodes(t, data)
	if len(gotCodes) != 8 || gotCodes[0] != "" || gotCodes[1] != "" || gotCodes[4] != "" {
		t.Fatalf("no-op tracing failed: %v", gotCodes)
	}
}

func TestHostTraceVerifierRestriction(t *testing.T) {
	tracer, recorder := testTraceRecorder(t)
	_, inst := newTraceFixture(t, compileTraceFixture(t), tracer)
	inst.ModuleContext().webhookVerify = true

	data, err := inst.InvokeNoArg(t.Context(), "run_flow")
	if err != nil {
		t.Fatal(err)
	}
	gotCodes := traceFixtureCodes(t, data)
	if len(gotCodes) != 8 {
		t.Fatalf("codes = %v", gotCodes)
	}
	for _, code := range gotCodes {
		if !strings.HasPrefix(code, abiv1.ErrCodeCapabilityDenied) {
			t.Errorf("verifier reached a restricted host function: %s", code)
		}
	}
	spans := spansByName(t, recorder)
	if spans["business.outer"] != nil || spans["leaked"] != nil {
		t.Error("verifier created a module span")
	}
	if crypto := spans["host.crypto.verify_hmac"]; crypto == nil || crypto.Status().Code == codes.Error {
		t.Error("verifier lost access to host.crypto")
	}
}

func TestModuleSpanValidationAndIsolation(t *testing.T) {
	tracer, recorder := testTraceRecorder(t)
	ctx, finish := beginInvocationTrace(t.Context(), tracer)
	defer finish()
	state := invocationTraceFromContext(ctx)
	outer := state.start(ctx, "outer", nil)
	inner := state.start(ctx, "inner", nil)

	otherCtx, otherFinish := beginInvocationTrace(ctx, tracer)
	defer otherFinish()
	other := invocationTraceFromContext(otherCtx)
	if err := other.end(outer, "", ""); err == nil || err.Code != abiv1.ErrCodeTraceSpanNotFound {
		t.Fatal("another invocation accepted the span")
	}

	if _, err := endModuleSpan(ctx, state, abiv1.TraceSpanEndInput{SpanID: outer, Status: "invalid"}); err == nil {
		t.Fatal("invalid status accepted")
	}
	if err := state.end(outer, "", ""); err != nil {
		t.Fatal(err)
	}
	_, host := (&Runtime{tracer: tracer}).startHostSpan(ctx, "host.test")
	host.End()
	if err := state.end(inner, "", ""); err != nil {
		t.Fatal(err)
	}
	spans := spansByName(t, recorder)
	if spans["host.test"].Parent().SpanID() != spans["inner"].SpanContext().SpanID() {
		t.Fatal("out-of-order end changed the innermost open span")
	}

	for _, value := range []any{nil, []string{"x"}, map[string]any{"x": true}, uint64(math.MaxUint64), math.Inf(1), math.NaN()} {
		if _, err := startModuleSpan(ctx, state, abiv1.TraceSpanStartInput{Name: "invalid", Attrs: map[string]any{"value": value}}); err == nil {
			t.Errorf("invalid attribute accepted: %v", value)
		}
	}
	if _, err := startModuleSpan(ctx, state, abiv1.TraceSpanStartInput{}); err == nil {
		t.Error("empty span name accepted")
	}
}

func TestModuleSpanNestedInvocation(t *testing.T) {
	tracer, recorder := testTraceRecorder(t)
	ctx, finish := beginInvocationTrace(t.Context(), tracer)
	defer finish()
	state := invocationTraceFromContext(ctx)
	outer := state.start(ctx, "outer", nil)

	hostCtx, host := (&Runtime{tracer: tracer}).startHostSpan(ctx, "host.callback")
	nestedCtx, nestedFinish := beginInvocationTrace(hostCtx, tracer)
	nested := invocationTraceFromContext(nestedCtx)
	nested.start(nestedCtx, "callback", nil)
	if err := nested.setAttr(outer, attribute.Bool("foreign", true)); err == nil {
		t.Fatal("nested invocation changed an outer span")
	}
	nestedFinish()
	host.End()

	_, next := (&Runtime{tracer: tracer}).startHostSpan(ctx, "host.next")
	next.End()
	if err := state.end(outer, "", ""); err != nil {
		t.Fatal(err)
	}
	spans := spansByName(t, recorder)
	if spans["callback"].Parent().SpanID() != spans["host.callback"].SpanContext().SpanID() {
		t.Error("callback did not inherit its enclosing host span")
	}
	if spans["host.next"].Parent().SpanID() != spans["outer"].SpanContext().SpanID() {
		t.Error("callback cleanup changed the outer invocation's stack")
	}
}
