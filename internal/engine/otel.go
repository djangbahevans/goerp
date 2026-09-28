package engine

import (
	"fmt"
	"net/http"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// otelMiddleware starts one root span per request, named after the
// resolved route's path template so spans group across path-param values.
// It runs right after routeResolutionMiddleware (auth-internals.md §9 step
// 3a) for every route class, so rate-limit, tenant, auth and MFA
// rejections still get a span and their error envelope a trace_id. Route
// resolution's own 404/405/503 have no template to name a span after and
// carry request_id alone.
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
			httperr.NoteSpan(ctx)

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
