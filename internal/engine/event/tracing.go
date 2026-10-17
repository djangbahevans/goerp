package event

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func EmissionAttributes(name string, version int, emitter string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("event.name", name),
		attribute.Int("event.version", version),
		attribute.String("event.emitter_module", emitter),
	}
}

func StartEmission(ctx context.Context, name string, version int, emitter string) (context.Context, trace.Span) {
	return otel.Tracer("goerp.event").Start(ctx, "event.emit",
		trace.WithAttributes(EmissionAttributes(name, version, emitter)...))
}

func FinishSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	span.End()
}

// SpanLink ignores incomplete context instead of fabricating a span identity.
func SpanLink(traceID, spanID string, flags uint8) []trace.Link {
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		return nil
	}

	sid, err := trace.SpanIDFromHex(spanID)
	if err != nil {
		return nil
	}

	return []trace.Link{{SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.TraceFlags(flags),
		Remote:     true,
	})}}
}
