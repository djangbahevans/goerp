package engine

import (
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// otelMiddleware is buildChain's innermost wrapper — engine-internals.md
// §6 step 12 — starting one root span per request under the engine's own
// tracer (goerp#326), named after the resolved route's own path template
// (not the raw URL path, so spans group correctly across different
// path-param values of the same route) rather than done per route class:
// unlike the tenant/auth steps above it in the chain, the pipeline
// diagram gives this step no route-class exemptions, so it runs
// unconditionally for every request that reaches it — EngineNative
// (builtin) routes included, since tracing them is exactly as valuable
// and doesn't touch the tenant/auth-resolution redundancy those routes'
// own no-op middleware exists to avoid.
//
// A request short-circuited earlier in the chain (404/405 from
// routeResolutionMiddleware, a rejected auth/tenant/MFA check) never
// reaches this middleware at all, since it sits innermost, immediately
// around buildDispatchHandler — so no span is ever started for those,
// consistent with the pipeline diagram's own "Module dispatch" placement
// for this step.
func otelMiddleware(tracer trace.Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr := routeResolutionFromContext(r.Context())
			if rr == nil {
				next.ServeHTTP(w, r)
				return
			}

			ctx, span := tracer.Start(r.Context(), rr.entry.PathTemplate, trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(r.Method),
				semconv.HTTPRoute(rr.entry.PathTemplate),
			))
			defer span.End()

			// A panic downstream (caught by the outer recoveryMiddleware,
			// which turns it into a 500) unwinds straight through this
			// deferred func without ever reaching the SetAttributes/
			// SetStatus calls below it — record it on the span and
			// re-panic so recoveryMiddleware still sees and handles it
			// exactly as if this middleware weren't here, but the span
			// reflects the real outcome instead of silently ending
			// without ever recording a status.
			defer func() {
				if panicVal := recover(); panicVal != nil {
					span.RecordError(fmt.Errorf("panic: %v", panicVal))
					span.SetStatus(codes.Error, "panic")
					panic(panicVal)
				}
			}()

			if tenantCtx := tenantFromContext(ctx); tenantCtx != nil {
				span.SetAttributes(attribute.String("tenant.id", tenantCtx.TenantID))
			}

			rec := &statusRecordingWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			span.SetAttributes(semconv.HTTPResponseStatusCode(rec.status))
			if rec.status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(rec.status))
			}
		})
	}
}

// statusRecordingWriter captures the status code an inner handler wrote
// so otelMiddleware can record it on the span after the fact —
// http.ResponseWriter itself exposes no way to read it back.
type statusRecordingWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusRecordingWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecordingWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.NewResponseController see through this wrapper to the
// underlying ResponseWriter's own http.Hijacker — otherwise a WebSocket
// upgrade (dispatchWSRoute) fails with http.ErrNotSupported on every
// request, since embedding http.ResponseWriter as an interface value only
// promotes its own method set, not Hijack.
func (w *statusRecordingWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
