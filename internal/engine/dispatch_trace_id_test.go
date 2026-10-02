package engine

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func traceFixtureModule(t *testing.T) (*Engine, *module.LoadedModule) {
	t.Helper()
	ctx := t.Context()

	wasmPath := filepath.Join(t.TempDir(), "tracefixture.wasm")
	cmd := exec.CommandContext(ctx, "go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/tracefixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/tracefixture: %v\n%s", err, out)
	}
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}

	rt, err := wasm.New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("create WASM runtime: %v", err)
	}
	// t.Context is canceled before cleanup; resource shutdown still needs a live context.
	cleanupCtx := context.WithoutCancel(ctx)
	t.Cleanup(func() { _ = rt.Close(cleanupCtx) })

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("compile WASM module: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(cleanupCtx) })

	// Cleanup runs in reverse order so the pool drains before the module and runtime close.
	pool := rt.NewPool("tracefixture", compiled, wasm.PoolConfig{MaxSize: 1, BorrowTimeout: 5 * time.Second})
	t.Cleanup(func() { pool.DrainAndClose(cleanupCtx, 5*time.Second) })

	return &Engine{wasmRuntime: rt}, &module.LoadedModule{
		Status:   module.StatusReady,
		Pool:     pool,
		Manifest: manifest.Manifest{Name: "tracefixture", Type: "standard"},
	}
}

func dispatchTraceFixture(t *testing.T, e *Engine, mod *module.LoadedModule, ctx context.Context) (seen string, envelope jsontext.Value) {
	t.Helper()
	ctx = withTenantContext(ctx, &tenantresolve.TenantContext{TenantID: "00000000-0000-0000-0000-000000000001", Slug: "acme"})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "00000000-0000-0000-0000-0000000000aa"})
	r := httptest.NewRequest(http.MethodGet, "/tracefixture/trace", nil).WithContext(ctx)
	rr := &routeResolution{entry: &route.RouteEntry{ModuleName: "tracefixture", PathTemplate: "/tracefixture/trace"}}

	w := httptest.NewRecorder()
	e.dispatchWASMRoute(ctx, w, r, rr, mod)

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				SeenTraceID *string `json:"seen_trace_id"`
			} `json:"details"`
			TraceID jsontext.Value `json:"trace_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if w.Code != http.StatusConflict || body.Error.Code != "tracefixture.seen" {
		t.Fatalf("got %d %s, want the fixture's 409 tracefixture.seen", w.Code, w.Body.String())
	}
	if body.Error.Details.SeenTraceID == nil {
		t.Fatalf("fixture omitted seen_trace_id: %s", w.Body.String())
	}
	return *body.Error.Details.SeenTraceID, body.Error.TraceID
}

func createWidgetTraceID(t *testing.T, ctx context.Context) (got string) {
	t.Helper()
	f := newDispatchORMFixture(t)
	// Isolate event jobs from other tests sharing system.river_job.
	f.tenantID = uuid.New().String()
	t.Cleanup(func() {
		if _, err := f.e.primaryDB.ExecContext(context.WithoutCancel(t.Context()),
			`DELETE FROM system.river_job WHERE kind = 'event_delivery' AND args->>'tenant_id' = $1`, f.tenantID); err != nil {
			t.Errorf("delete fixture event jobs: %v", err)
		}
	})

	body, err := json.Marshal(map[string]any{"name": "Widget A"})
	if err != nil {
		t.Fatal(err)
	}
	r := f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil)
	ctx = trace.ContextWithSpan(r.Context(), trace.SpanFromContext(ctx))

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, r.WithContext(ctx))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}

	if err := f.e.primaryDB.QueryRowContext(t.Context(),
		`SELECT args->>'trace_id' FROM system.river_job WHERE kind = 'event_delivery' AND args->>'tenant_id' = $1 AND args->>'event_name' = 'orm.record.created'`, f.tenantID,
	).Scan(&got); err != nil {
		t.Fatalf("read the create's event job: %v", err)
	}
	return got
}

func dispatchTraceCases(t *testing.T) []struct {
	name    string
	ctx     context.Context
	traceID string
} {
	t.Helper()
	noopCtx, noopSpan := noop.NewTracerProvider().Tracer("test").Start(t.Context(), "test")
	t.Cleanup(func() { noopSpan.End() })
	_, tp := newRecordingTracer(t)
	recordingCtx, recordingSpan := tp.Tracer("test").Start(t.Context(), "test")
	t.Cleanup(func() { recordingSpan.End() })
	nonRecordingSpan := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{1},
		Remote:  true,
	})
	return []struct {
		name    string
		ctx     context.Context
		traceID string
	}{
		{name: "no_span", ctx: t.Context()},
		{name: "noop_tracer", ctx: noopCtx},
		{name: "recording_span", ctx: recordingCtx, traceID: recordingSpan.SpanContext().TraceID().String()},
		{name: "non_recording_span", ctx: trace.ContextWithSpanContext(t.Context(), nonRecordingSpan), traceID: nonRecordingSpan.TraceID().String()},
	}
}

func TestDispatchWASMRoute_TraceID(t *testing.T) {
	e, mod := traceFixtureModule(t)
	for _, tc := range dispatchTraceCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			seen, envelope := dispatchTraceFixture(t, e, mod, tc.ctx)
			if seen != tc.traceID {
				t.Errorf("module trace_id = %q, want %q", seen, tc.traceID)
			}
			if tc.traceID == "" {
				if len(envelope) != 0 {
					t.Errorf("envelope trace_id = %s, want it omitted", envelope)
				}
			} else {
				var envelopeTraceID string
				if err := json.Unmarshal(envelope, &envelopeTraceID); err != nil {
					t.Fatalf("decode envelope trace_id: %v", err)
				}
				if envelopeTraceID != tc.traceID {
					t.Errorf("envelope trace_id = %q, want %q", envelopeTraceID, tc.traceID)
				}
			}
		})
	}
}

func TestDispatchORMRoute_TraceID(t *testing.T) {
	for _, tc := range dispatchTraceCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			if got := createWidgetTraceID(t, tc.ctx); got != tc.traceID {
				t.Errorf("event job trace_id = %q, want %q", got, tc.traceID)
			}
		})
	}
}
