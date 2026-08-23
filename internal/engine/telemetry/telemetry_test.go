package telemetry

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

func TestSetupTracing_EmptyEndpointReturnsNoop(t *testing.T) {
	ctx := context.Background()
	tp, tr, err := SetupTracing(ctx, Config{
		Endpoint:    "",
		ServiceName: "test-service",
		Environment: "test",
	})
	if err != nil {
		t.Fatalf("SetupTracing with empty endpoint returned error: %v", err)
	}
	if tp != nil {
		t.Errorf("expected nil TracerProvider for empty endpoint, got %v", tp)
	}
	if tr == nil {
		t.Fatal("expected non-nil tracer, got nil")
	}

	_, span := tr.Start(ctx, "test-span")
	span.End()
}

func TestSetupTracing_ValidEndpoint(t *testing.T) {
	ctx := context.Background()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	srv := grpc.NewServer()
	go func() { _ = srv.Serve(ln) }()
	defer srv.Stop()

	tp, tr, err := SetupTracing(ctx, Config{
		Endpoint:    ln.Addr().String(),
		ServiceName: "test-service",
		Environment: "development",
		Insecure:    true,
	})
	if err != nil {
		t.Fatalf("SetupTracing returned error: %v", err)
	}
	if tp == nil {
		t.Fatal("expected non-nil TracerProvider, got nil")
	}
	if tr == nil {
		t.Fatal("expected non-nil tracer, got nil")
	}

	_, span := tr.Start(ctx, "root-span")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tp.Shutdown(shutdownCtx); err != nil {
		t.Errorf("tp.Shutdown returned error: %v", err)
	}
}

func TestSetupTracing_W3CTraceContextPropagation(t *testing.T) {
	ctx := context.Background()
	_, _, err := SetupTracing(ctx, Config{
		Endpoint:    "",
		ServiceName: "test-service",
		Environment: "test",
	})
	if err != nil {
		t.Fatalf("SetupTracing returned error: %v", err)
	}

	propagator := otel.GetTextMapPropagator()
	if propagator == nil {
		t.Fatal("expected global TextMapPropagator to be set, got nil")
	}

	// Test extracting traceparent header
	rawHeader := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost", nil)
	req.Header.Set("traceparent", rawHeader)

	extractedCtx := propagator.Extract(req.Context(), propagation.HeaderCarrier(req.Header))
	spanCtx := trace.SpanContextFromContext(extractedCtx)

	if !spanCtx.IsValid() {
		t.Fatal("expected valid SpanContext after extraction")
	}
	if spanCtx.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("TraceID = %s, want 4bf92f3577b34da6a3ce929d0e0e4736", spanCtx.TraceID().String())
	}
	if spanCtx.SpanID().String() != "00f067aa0ba902b7" {
		t.Errorf("SpanID = %s, want 00f067aa0ba902b7", spanCtx.SpanID().String())
	}

	// Test injecting into outbound headers
	outReq, _ := http.NewRequestWithContext(extractedCtx, http.MethodGet, "http://remote", nil)
	propagator.Inject(extractedCtx, propagation.HeaderCarrier(outReq.Header))

	if got := outReq.Header.Get("traceparent"); got != rawHeader {
		t.Errorf("injected traceparent = %q, want %q", got, rawHeader)
	}
}
