package eventdelivery

import (
	"context"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func startSubscriberSpan(ctx context.Context, env event.Envelope, moduleName string, attempt int, async, replay bool) (context.Context, trace.Span) {
	opts := []trace.SpanStartOption{
		trace.WithAttributes(
			attribute.String("event.name", env.Name),
			attribute.Int("event.version", env.Version),
			attribute.String("event.subscriber_module", moduleName),
			attribute.Int("event.attempt", attempt),
			attribute.Bool("event.replay", replay),
		),
	}
	if async {
		opts = append(opts, trace.WithNewRoot(), trace.WithLinks(event.SpanLink(env.TraceID, env.SpanID, env.TraceFlags)...))
	}

	return otel.Tracer("goerp.event").Start(ctx, "event.subscriber", opts...)
}
