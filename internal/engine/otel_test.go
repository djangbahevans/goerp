package engine

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/coder/websocket"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/route"
)

// newRecordingTracer returns a real *sdktrace.TracerProvider exporting to
// an in-memory exporter, so tests can assert against real recorded spans
// rather than a mock — same convention the rest of this codebase's
// integration tests favor.
func newRecordingTracer(t *testing.T) (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })
	return exporter, tp
}

func TestOtelMiddleware_StartsSpanNamedAfterRouteTemplate(t *testing.T) {
	exporter, tp := newRecordingTracer(t)
	rr := &routeResolution{entry: &route.RouteEntry{PathTemplate: "/widgets/{id}"}}

	h := otelMiddleware(tp.Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/widgets/123", nil)
	req = req.WithContext(withRouteResolution(req.Context(), rr))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != "/widgets/{id}" {
		t.Errorf("span name = %q, want %q (the route template, not the raw path)", span.Name, "/widgets/{id}")
	}

	var gotMethod, gotRoute string
	var gotStatus int64
	for _, attr := range span.Attributes {
		switch attr.Key {
		case semconv.HTTPRequestMethodKey:
			gotMethod = attr.Value.AsString()
		case semconv.HTTPRouteKey:
			gotRoute = attr.Value.AsString()
		case semconv.HTTPResponseStatusCodeKey:
			gotStatus = attr.Value.AsInt64()
		}
	}
	if gotMethod != http.MethodGet {
		t.Errorf("http.request.method = %q, want %q", gotMethod, http.MethodGet)
	}
	if gotRoute != "/widgets/{id}" {
		t.Errorf("http.route = %q, want %q", gotRoute, "/widgets/{id}")
	}
	if gotStatus != http.StatusOK {
		t.Errorf("http.response.status_code = %d, want %d", gotStatus, http.StatusOK)
	}
}

func TestOtelMiddleware_ServerErrorSetsSpanStatusError(t *testing.T) {
	exporter, tp := newRecordingTracer(t)
	rr := &routeResolution{entry: &route.RouteEntry{PathTemplate: "/widgets"}}

	h := otelMiddleware(tp.Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
	req = req.WithContext(withRouteResolution(req.Context(), rr))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Errorf("span status = %v, want Error", spans[0].Status.Code)
	}
}

func TestOtelMiddleware_NoRouteResolutionIsNoOp(t *testing.T) {
	exporter, tp := newRecordingTracer(t)

	var called bool
	h := otelMiddleware(tp.Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Error("next handler was not called")
	}
	if len(exporter.GetSpans()) != 0 {
		t.Errorf("got %d spans, want 0 — a request that never resolved a route should never start a span", len(exporter.GetSpans()))
	}
}

// The span starts before tenant resolution, so tenantResolutionMiddleware
// is what sets tenant.id on it.
func TestBuildChain_TenantResolutionAddsTenantAttributeToSpan(t *testing.T) {
	f := newChainFixture(t)
	exporter, tp := newRecordingTracer(t)
	h := f.tracedChain(nil, tp.Tracer("test"))

	req := httptest.NewRequest(http.MethodGet, "/widgets/items", nil)
	req.Host = f.domain
	req.Header.Set("Authorization", "Bearer "+f.issueToken(t))
	h.ServeHTTP(httptest.NewRecorder(), req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	var gotTenantID string
	for _, attr := range spans[0].Attributes {
		if attr.Key == "tenant.id" {
			gotTenantID = attr.Value.AsString()
		}
	}
	if gotTenantID != f.tenantID {
		t.Errorf("tenant.id = %q, want %q", gotTenantID, f.tenantID)
	}
}

// TestBuildChain_AuthRejectionCarriesTraceID proves an auth rejection,
// which never reaches dispatch, still gets a span and so a trace_id.
func TestBuildChain_AuthRejectionCarriesTraceID(t *testing.T) {
	f := newChainFixture(t)
	exporter, tp := newRecordingTracer(t)
	h := f.tracedChain(nil, tp.Tracer("test"))

	req := httptest.NewRequest(http.MethodGet, "/widgets/items", nil)
	req.Host = f.domain
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
	var env httperr.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Error.RequestID != w.Header().Get(requestIDHeader) {
		t.Errorf("request_id = %q, want X-Request-Id %q", env.Error.RequestID, w.Header().Get(requestIDHeader))
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if want := spans[0].SpanContext.TraceID().String(); env.Error.TraceID != want {
		t.Errorf("trace_id = %q, want %q", env.Error.TraceID, want)
	}
}

// TestOtelMiddleware_PanicIsRecordedOnSpanThenRePanics proves a panic
// downstream (as recoveryMiddleware, the outermost stage, would catch in
// the real chain) still gets recorded on the span with an Error status
// before propagating — otherwise the span would simply end with no
// recorded status at all, not reflecting the real 500 recoveryMiddleware
// turns it into.
func TestOtelMiddleware_PanicIsRecordedOnSpanThenRePanics(t *testing.T) {
	exporter, tp := newRecordingTracer(t)
	rr := &routeResolution{entry: &route.RouteEntry{PathTemplate: "/widgets"}}

	h := otelMiddleware(tp.Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
	req = req.WithContext(withRouteResolution(req.Context(), rr))
	w := httptest.NewRecorder()

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected otelMiddleware to re-panic, but it didn't")
			}
		}()
		h.ServeHTTP(w, req)
	}()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Errorf("span status = %v, want Error", spans[0].Status.Code)
	}
	if len(spans[0].Events) == 0 {
		t.Error("expected the panic to be recorded as a span event via RecordError, got none")
	}
}

// TestOtelMiddleware_PreservesHijackerForWebSocketUpgrade proves
// statusRecordingWriter's Unwrap method actually lets a WebSocket upgrade
// (dispatchWSRoute, goerp#616) succeed through this middleware — without
// it, http.NewResponseController can't see through the wrapper to the
// underlying ResponseWriter's http.Hijacker, and every /_ws request in
// the real chain (which always passes through otelMiddleware) would fail.
// httptest.NewRecorder can't exercise this — Hijack needs a real network
// connection — so this dials a real httptest.NewServer.
func TestOtelMiddleware_PreservesHijackerForWebSocketUpgrade(t *testing.T) {
	rr := &routeResolution{entry: &route.RouteEntry{PathTemplate: "/_ws"}}

	h := otelMiddleware(noop.NewTracerProvider().Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept: %v", err)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(withRouteResolution(r.Context(), rr)))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Listener.Addr().String(), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
}

func TestOtelMiddleware_NoopTracerDoesNotPanic(t *testing.T) {
	rr := &routeResolution{entry: &route.RouteEntry{PathTemplate: "/widgets"}}
	h := otelMiddleware(noop.NewTracerProvider().Tracer("test"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
	req = req.WithContext(withRouteResolution(req.Context(), rr))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
