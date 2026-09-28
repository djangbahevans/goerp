package httperr

import (
	"context"
	"encoding/json/v2"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// tracedContext returns a context carrying requestID and a real recorded
// span, plus that span's trace id.
func tracedContext(t *testing.T, requestID string) (context.Context, string) {
	t.Helper()
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("test").Start(WithRequestID(t.Context(), requestID), "test")
	t.Cleanup(func() { span.End() })
	return ctx, span.SpanContext().TraceID().String()
}

func decode(t *testing.T, body []byte) Envelope {
	t.Helper()
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return env
}

func TestWrite_IncludesRequestIDAndTraceID(t *testing.T) {
	ctx, traceID := tracedContext(t, "req-1")
	w := httptest.NewRecorder()
	Write(ctx, w, 404, "not_found", "not found")

	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	env := decode(t, w.Body.Bytes())
	if env.Error.Code != "not_found" || env.Error.Message != "not found" {
		t.Errorf("error = %+v, want code/message not_found/not found", env.Error)
	}
	if env.Error.RequestID != "req-1" {
		t.Errorf("request_id = %q, want %q", env.Error.RequestID, "req-1")
	}
	if env.Error.TraceID != traceID {
		t.Errorf("trace_id = %q, want %q", env.Error.TraceID, traceID)
	}
}

func TestWrite_FieldOrderMatchesDesign(t *testing.T) {
	ctx, traceID := tracedContext(t, "req-1")
	w := httptest.NewRecorder()
	WriteDetails(ctx, w, 422, "x.bad", "bad", map[string]any{"field": "a"})

	want := `{"error":{"code":"x.bad","message":"bad","details":{"field":"a"},"request_id":"req-1","trace_id":"` + traceID + `"}}`
	if got := w.Body.String(); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWrite_OmitsIDsWhenContextHasNone(t *testing.T) {
	w := httptest.NewRecorder()
	Write(t.Context(), w, 400, "invalid_request", "bad")

	body := w.Body.String()
	if strings.Contains(body, "request_id") || strings.Contains(body, "trace_id") {
		t.Errorf("body = %s, want no request_id/trace_id", body)
	}
}

func TestWrite_OmitsTraceIDForInvalidSpan(t *testing.T) {
	ctx := trace.ContextWithSpanContext(WithRequestID(t.Context(), "req-1"), trace.SpanContext{})
	w := httptest.NewRecorder()
	Write(ctx, w, 404, "route_not_found", "no route")

	env := decode(t, w.Body.Bytes())
	if env.Error.RequestID != "req-1" {
		t.Errorf("request_id = %q, want req-1", env.Error.RequestID)
	}
	if strings.Contains(w.Body.String(), "trace_id") {
		t.Errorf("body = %s, want no trace_id", w.Body.String())
	}
}

// encoding/json/v2's MarshalWrite doesn't escape HTML/JS-unsafe characters
// by default the way v1's Encoder did — the writer passes explicit options
// to keep that parity (goerp#530).
func TestWrite_EscapesHTMLUnsafeCharacters(t *testing.T) {
	w := httptest.NewRecorder()
	Write(t.Context(), w, 400, "invalid_request", "<script>&</script>")

	wantEscaped := "\\u003cscript\\u003e\\u0026\\u003c/script\\u003e"
	if !strings.Contains(w.Body.String(), wantEscaped) {
		t.Errorf("body = %s, want it to contain %s", w.Body.String(), wantEscaped)
	}
}

func TestAnnotate_SetsIDsAndKeepsModuleFields(t *testing.T) {
	ctx, traceID := tracedContext(t, "req-1")
	in := []byte(`{"data":null,"error":{"code":"sales.x","message":"m","details":{"b":2,"a":1},"hint":"h","request_id":"spoofed"}}`)

	got := string(Annotate(ctx, in))
	want := `{"error":{"code":"sales.x","message":"m","details":{"b":2,"a":1},"request_id":"req-1","trace_id":"` + traceID + `","hint":"h"},"data":null}`
	if got != want {
		t.Errorf("Annotate() = %s, want %s", got, want)
	}
}

func TestAnnotate_NonEnvelopeBodyUnchanged(t *testing.T) {
	ctx, _ := tracedContext(t, "req-1")
	for _, in := range []string{``, `not json`, `[1,2]`, `{"data":{"id":1}}`, `{"error":null}`} {
		if got := string(Annotate(ctx, []byte(in))); got != in {
			t.Errorf("Annotate(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestAnnotate_KeepsEmptyAndMissingMembersAsSent(t *testing.T) {
	ctx, traceID := tracedContext(t, "req-1")
	ids := `"request_id":"req-1","trace_id":"` + traceID + `"`
	for in, want := range map[string]string{
		`{"error":{"code":"validation_failed","details":[]}}`: `{"error":{"code":"validation_failed","details":[],` + ids + `}}`,
		`{"error":{"code":"x","message":"","details":{}}}`:    `{"error":{"code":"x","message":"","details":{},` + ids + `}}`,
		`{"error":{"code":"x","details":null}}`:               `{"error":{"code":"x","details":null,` + ids + `}}`,
	} {
		if got := string(Annotate(ctx, []byte(in))); got != want {
			t.Errorf("Annotate(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestTrackIDs_OuterContextSeesIDsSetOnInnerOnes(t *testing.T) {
	outer := TrackIDs(t.Context())

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	spanCtx, span := tp.Tracer("test").Start(WithRequestID(outer, "req-2"), "test")
	defer span.End()
	NoteSpan(spanCtx)

	w := httptest.NewRecorder()
	Write(outer, w, 500, "internal_error", "internal server error")
	env := decode(t, w.Body.Bytes())
	if env.Error.RequestID != "req-2" {
		t.Errorf("request_id = %q, want req-2", env.Error.RequestID)
	}
	if want := span.SpanContext().TraceID().String(); env.Error.TraceID != want {
		t.Errorf("trace_id = %q, want %q", env.Error.TraceID, want)
	}
}

func TestAnnotate_OverwritesNonStringModuleIDs(t *testing.T) {
	ctx, traceID := tracedContext(t, "req-1")
	in := []byte(`{"error":{"code":"x","request_id":123,"trace_id":{"spoofed":true}}}`)

	got := string(Annotate(ctx, in))
	want := `{"error":{"code":"x","request_id":"req-1","trace_id":"` + traceID + `"}}`
	if got != want {
		t.Errorf("Annotate() = %s, want %s", got, want)
	}
}

func TestAnnotate_ToleratesDuplicateNamesAndInvalidUTF8(t *testing.T) {
	ctx, _ := tracedContext(t, "req-1")
	for _, in := range []string{
		`{"error":{"code":"x","code":"y"}}`,
		"{\"error\":{\"code\":\"x\",\"message\":\"bad \xff\"}}",
	} {
		env := decode(t, Annotate(ctx, []byte(in)))
		if env.Error.RequestID != "req-1" {
			t.Errorf("Annotate(%q) request_id = %q, want req-1", in, env.Error.RequestID)
		}
	}
}

func TestWriteDetails_SortsMapKeys(t *testing.T) {
	w := httptest.NewRecorder()
	WriteDetails(t.Context(), w, 409, "x", "m", map[string]any{"z": 1, "a": 2, "m": 3})

	want := `{"error":{"code":"x","message":"m","details":{"a":2,"m":3,"z":1}}}`
	if got := w.Body.String(); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}
