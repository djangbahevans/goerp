package eventdelivery

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func eventTraceExporter(t *testing.T) (trace.Tracer, *tracetest.InMemoryExporter) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	return provider.Tracer("test"), exporter
}

func TestSubscriberTracing(t *testing.T) {
	for _, sync := range []bool{false, true} {
		for _, status := range []int32{0, 1, 2} {
			t.Run(fmt.Sprintf("sync=%t/status=%d", sync, status), func(t *testing.T) {
				tracer, exporter := eventTraceExporter(t)
				ctx, emit := tracer.Start(t.Context(), "emission")
				defer emit.End()
				source := emit.SpanContext()
				w := newTestSubscriberWorker(t, buildHandleEventConstStatusModule(status))
				env := event.Envelope{
					Name:       testEventName,
					Version:    1,
					TraceID:    source.TraceID().String(),
					SpanID:     source.SpanID().String(),
					TraceFlags: uint8(source.TraceFlags()),
				}
				if sync {
					payload, err := env.Marshal()
					if err != nil {
						t.Fatal(err)
					}
					d := &SyncDispatcher{ModuleRegistry: w.ModuleRegistry, Invoker: w.Invoker}
					if got, err := d.DispatchSync(ctx, testEventModuleName, testHandlerName, payload); err != nil || got != status {
						t.Fatalf("dispatch = %d, %v", got, err)
					}
				} else {
					job := &river.Job[jobqueue.SubscriberDeliveryArgs]{
						JobRow: &rivertype.JobRow{Attempt: 3},
						Args: jobqueue.SubscriberDeliveryArgs{
							EventName:    env.Name,
							EventVersion: env.Version,
							ModuleName:   testEventModuleName,
							HandlerName:  testHandlerName,
							TraceID:      env.TraceID,
							SpanID:       env.SpanID,
							TraceFlags:   env.TraceFlags,
						},
					}
					if err := w.Work(ctx, job); (err != nil) != (status != 0) {
						t.Fatalf("delivery error = %v", err)
					}
				}

				spans := exporter.GetSpans()
				if len(spans) != 1 || spans[0].Name != "event.subscriber" {
					t.Fatalf("subscriber spans = %+v", spans)
				}
				span := spans[0]
				if sync {
					if !span.Parent.Equal(source) || len(span.Links) != 0 {
						t.Fatalf("sync parent = %v, links = %v", span.Parent, span.Links)
					}
				} else if span.Parent.IsValid() || span.SpanContext.TraceID() == source.TraceID() || len(span.Links) != 1 || span.Links[0].SpanContext.TraceID() != source.TraceID() || span.Links[0].SpanContext.SpanID() != source.SpanID() || !span.Links[0].SpanContext.IsSampled() {
					t.Fatalf("async parent = %v, span = %v, links = %v", span.Parent, span.SpanContext, span.Links)
				}
				if (span.Status.Code == codes.Error) != (status != 0) {
					t.Fatalf("span status = %v", span.Status)
				}
				attrs := make(map[string]any)
				for _, attr := range span.Attributes {
					attrs[string(attr.Key)] = attr.Value.AsInterface()
				}
				attempt := int64(3)
				if sync {
					attempt = 1
				}
				if attrs["event.name"] != testEventName || attrs["event.version"] != int64(1) || attrs["event.subscriber_module"] != testEventModuleName || attrs["event.attempt"] != attempt {
					t.Fatalf("attributes = %v", attrs)
				}
			})
		}
	}
}

func TestSubscriberTracingIncompleteOrigin(t *testing.T) {
	for _, env := range []event.Envelope{
		{},
		{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"},
		{TraceID: "invalid", SpanID: "00f067aa0ba902b7"},
		{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "0000000000000000"},
	} {
		tracer, exporter := eventTraceExporter(t)
		ctx, unrelated := tracer.Start(t.Context(), "worker")
		_, span := startSubscriberSpan(ctx, env, "subscriber", 1, true, false)
		span.End()
		unrelated.End()
		got := exporter.GetSpans()[0]
		if got.Parent.IsValid() || len(got.Links) != 0 {
			t.Fatalf("incomplete origin: parent = %v, links = %v", got.Parent, got.Links)
		}
	}
}

func TestSubscriberTracingNonRecordingOrigin(t *testing.T) {
	_, exporter := eventTraceExporter(t)
	tid, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	source := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid})
	ctx := trace.ContextWithSpanContext(t.Context(), source)
	_, span := startSubscriberSpan(ctx, event.Envelope{TraceID: tid.String(), SpanID: sid.String()}, "subscriber", 1, true, false)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Parent.IsValid() || len(spans[0].Links) != 1 {
		t.Fatalf("non-recording origin delivery = %+v", spans)
	}
	link := spans[0].Links[0].SpanContext
	if link.TraceID() != tid || link.SpanID() != sid || link.IsSampled() {
		t.Fatalf("origin context = %v, want unsampled %v", link, source)
	}
}

func TestFanOutAndReplayPreserveEmissionLink(t *testing.T) {
	_, exporter := eventTraceExporter(t)
	w, tenants, conn, ctx := newTestWorker(t, testEventName, []manifest.EventSubscription{
		{Name: testEventName, Handler: testHandlerName, Async: true},
	})
	tn := newTestTenant(t, tenants, conn, uniqueSlug(t))
	ctx, emission := event.StartEmission(ctx, testEventName, 1, "sales")
	source := emission.SpanContext()
	emittedAt := time.Now().UTC().Truncate(time.Second)
	id := uniqueEventID(t, conn)
	if err := w.Work(ctx, &river.Job[jobqueue.EventDeliveryArgs]{Args: jobqueue.EventDeliveryArgs{
		EventID:       id,
		EventName:     testEventName,
		EventVersion:  1,
		EmitterModule: "sales",
		TenantID:      tn.ID,
		TraceID:       source.TraceID().String(),
		SpanID:        source.SpanID().String(),
		TraceFlags:    uint8(source.TraceFlags()),
		EmittedAt:     emittedAt,
		Payload:       []byte{},
	}}); err != nil {
		t.Fatal(err)
	}
	emission.End()
	exporter.Reset()

	replay := &EventsReplayWorker{ModuleRegistry: w.ModuleRegistry, TenantStore: tenants, Pool: conn}
	if err := replay.Work(ctx, &river.Job[jobqueue.EventsReplayArgs]{Args: jobqueue.EventsReplayArgs{
		Tenant:     tn.Slug,
		EventNames: []string{testEventName},
		BatchSize:  100,
		From:       emittedAt.Add(-time.Second),
		To:         emittedAt.Add(time.Second),
	}}); err != nil {
		t.Fatal(err)
	}

	rows, err := conn.QueryContext(ctx, `SELECT args FROM system.river_job WHERE kind='subscriber_delivery' AND args->>'event_id'=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var deliveries []jobqueue.SubscriberDeliveryArgs
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var args jobqueue.SubscriberDeliveryArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
		deliveries = append(deliveries, args)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 2 || deliveries[0].Replay == deliveries[1].Replay {
		t.Fatalf("fan-out and replay jobs = %+v", deliveries)
	}
	subscriber := newTestSubscriberWorker(t, buildHandleEventConstStatusModule(0))
	for _, args := range deliveries {
		if err := runSubscriberWork(t, subscriber, args); err != nil {
			t.Fatal(err)
		}
	}
	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("delivery spans = %+v, want fan-out and replay", spans)
	}
	for _, span := range spans {
		if span.Parent.IsValid() || len(span.Links) != 1 || span.Links[0].SpanContext.TraceID() != source.TraceID() || span.Links[0].SpanContext.SpanID() != source.SpanID() {
			t.Fatalf("delivery lost emission link: %+v", span)
		}
	}
}
