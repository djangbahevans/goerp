package engine

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func tracedChain(t *testing.T) (http.Handler, *tracetest.InMemoryExporter) {
	t.Helper()
	exporter, tp := newRecordingTracer(t)

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"contacts": {
			Manifest: manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{
				{Method: http.MethodGet, Path: "/ping"},
				{Method: http.MethodGet, Path: "/private", Auth: "required"},
			},
		},
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	var h = (&Engine{}).buildDispatchHandler(nil)
	h = routeAuthMiddleware()(h)
	h = otelMiddleware(tp.Tracer("test"))(h)
	h = routeResolutionMiddleware(reg)(h)
	h = requestIDMiddleware()(h)
	return h, exporter
}

func serveEnvelope(t *testing.T, h http.Handler, path string) (*httptest.ResponseRecorder, httperr.Envelope) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	var env httperr.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if env.Error.RequestID == "" || env.Error.RequestID != w.Header().Get(requestIDHeader) {
		t.Errorf("request_id = %q, want it to match X-Request-Id %q", env.Error.RequestID, w.Header().Get(requestIDHeader))
	}
	return w, env
}

func onlySpanTraceID(t *testing.T, exporter *tracetest.InMemoryExporter) string {
	t.Helper()
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	return spans[0].SpanContext.TraceID().String()
}

func TestErrorEnvelope_ModuleDispatchErrorCarriesRequestAndTraceID(t *testing.T) {
	h, exporter := tracedChain(t)

	w, env := serveEnvelope(t, h, "/contacts/ping")

	if w.Code != http.StatusServiceUnavailable || env.Error.Code != "module_unavailable" {
		t.Fatalf("got %d %q, want 503 module_unavailable", w.Code, env.Error.Code)
	}
	if want := onlySpanTraceID(t, exporter); env.Error.TraceID != want {
		t.Errorf("trace_id = %q, want span's %q", env.Error.TraceID, want)
	}
}

func TestErrorEnvelope_PreDispatchRejectionAfterSpanCarriesTraceID(t *testing.T) {
	h, exporter := tracedChain(t)

	w, env := serveEnvelope(t, h, "/contacts/private")

	if w.Code != http.StatusUnauthorized || env.Error.Code != "unauthenticated" {
		t.Fatalf("got %d %q, want 401 unauthenticated", w.Code, env.Error.Code)
	}
	if want := onlySpanTraceID(t, exporter); env.Error.TraceID != want {
		t.Errorf("trace_id = %q, want span's %q", env.Error.TraceID, want)
	}
}

func TestErrorEnvelope_RouteNotFoundHasRequestIDButNoTraceID(t *testing.T) {
	h, exporter := tracedChain(t)

	w, env := serveEnvelope(t, h, "/nowhere")

	if w.Code != http.StatusNotFound || env.Error.Code != "route_not_found" {
		t.Fatalf("got %d %q, want 404 route_not_found", w.Code, env.Error.Code)
	}
	if strings.Contains(w.Body.String(), "trace_id") {
		t.Errorf("body = %s, want no trace_id before any span starts", w.Body.String())
	}
	if n := len(exporter.GetSpans()); n != 0 {
		t.Errorf("got %d spans, want 0", n)
	}
}

// writeResponse is where both a WASM handler's and an EnableOps route's
// error body get their ids, so both stay byte-identical.
func TestWriteResponse_AnnotatesModuleErrorBodyOnly(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })
	ctx, span := tp.Tracer("test").Start(httperr.WithRequestID(t.Context(), "req-1"), "test")
	defer span.End()
	traceID := span.SpanContext().TraceID().String()

	w := httptest.NewRecorder()
	writeResponse(ctx, w, EngineResponse{StatusCode: 409, Body: []byte(`{"error":{"code":"sales.order.conflict","message":"m","details":{"id":"1"}}}`)})
	want := `{"error":{"code":"sales.order.conflict","message":"m","details":{"id":"1"},"request_id":"req-1","trace_id":"` + traceID + `"}}`
	if got := w.Body.String(); got != want {
		t.Errorf("error body = %s, want %s", got, want)
	}

	w = httptest.NewRecorder()
	writeResponse(ctx, w, EngineResponse{StatusCode: 404, Headers: map[string]string{"Content-Length": "40"}, Body: []byte(`{"error":{"code":"x","message":"m"}}`)})
	if cl := w.Header().Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length = %q after the body grew, want it dropped", cl)
	}

	success := `{"data":{"error":{"code":"not-an-error"}}}`
	w = httptest.NewRecorder()
	writeResponse(ctx, w, EngineResponse{StatusCode: 200, Body: []byte(success)})
	if got := w.Body.String(); got != success {
		t.Errorf("success body = %s, want it unchanged", got)
	}
}

// recoveryMiddleware runs outside requestIDMiddleware and otelMiddleware,
// yet its panic 500 still carries both ids.
func TestErrorEnvelope_PanicCarriesRequestAndTraceID(t *testing.T) {
	exporter, tp := newRecordingTracer(t)
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"contacts": {
			Manifest:       manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{{Method: http.MethodGet, Path: "/ping"}},
		},
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	var h http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	h = otelMiddleware(tp.Tracer("test"))(h)
	h = routeResolutionMiddleware(reg)(h)
	h = requestIDMiddleware()(h)
	h = recoveryMiddleware()(h)

	w, env := serveEnvelope(t, h, "/contacts/ping")

	if w.Code != http.StatusInternalServerError || env.Error.Code != "internal_error" {
		t.Fatalf("got %d %q, want 500 internal_error", w.Code, env.Error.Code)
	}
	if want := onlySpanTraceID(t, exporter); env.Error.TraceID != want {
		t.Errorf("trace_id = %q, want span's %q", env.Error.TraceID, want)
	}
}
