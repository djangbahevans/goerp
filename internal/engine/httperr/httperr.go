// Package httperr writes the engine's {"error": {...}} envelope
// (erp-design.md §4.4.5), stamping each one with the request's request_id
// and, when a span is active, its trace_id.
package httperr

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/trace"
)

// Envelope is the {"error": {...}} wire shape.
type Envelope struct {
	Error Body `json:"error"`
}

// Body is the envelope's error object. RequestID/TraceID are omitted only
// when ctx carries neither — a handler unit-tested in isolation, or (for
// TraceID) a request rejected before otelMiddleware started its span.
type Body struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   any    `json:"details,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
}

type requestIDContextKey struct{}

type trackedIDsContextKey struct{}

// trackedIDs carries ids set deeper in the chain back out to
// recoveryMiddleware, whose context predates both of them.
type trackedIDs struct {
	requestID string
	traceID   string
}

// TrackIDs returns ctx with a slot that WithRequestID and NoteSpan fill
// in, so Write on ctx itself (or anything derived from it) still finds
// ids that were only set on a context derived later.
func TrackIDs(ctx context.Context) context.Context {
	return context.WithValue(ctx, trackedIDsContextKey{}, &trackedIDs{})
}

func tracked(ctx context.Context) *trackedIDs {
	t, _ := ctx.Value(trackedIDsContextKey{}).(*trackedIDs)
	return t
}

// WithRequestID stashes the request's id for RequestIDFromContext — set
// once by the engine's requestIDMiddleware.
func WithRequestID(ctx context.Context, id string) context.Context {
	if t := tracked(ctx); t != nil {
		t.requestID = id
	}
	return context.WithValue(ctx, requestIDContextKey{}, id)
}

// NoteSpan records ctx's active span's trace id in ctx's TrackIDs slot —
// called by otelMiddleware once it starts the request's span.
func NoteSpan(ctx context.Context) {
	if t := tracked(ctx); t != nil {
		t.traceID = spanTraceID(ctx)
	}
}

// RequestIDFromContext returns the id WithRequestID stashed, or "".
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDContextKey{}).(string); ok {
		return id
	}
	if t := tracked(ctx); t != nil {
		return t.requestID
	}
	return ""
}

// TraceIDFromContext returns the active span's trace id, or "" when ctx
// carries no valid span (no span started yet, or a noop tracer).
func TraceIDFromContext(ctx context.Context) string {
	if id := spanTraceID(ctx); id != "" {
		return id
	}
	if t := tracked(ctx); t != nil {
		return t.traceID
	}
	return ""
}

func spanTraceID(ctx context.Context) string {
	if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
		return sc.TraceID().String()
	}
	return ""
}

// Write writes a {code, message} error envelope with status, reading
// request_id/trace_id from ctx.
func Write(ctx context.Context, w http.ResponseWriter, status int, code, message string) {
	WriteDetails(ctx, w, status, code, message, nil)
}

// WriteDetails is Write plus an optional details value — e.g.
// billing.module_not_available's "module"/"upgrade_url" fields
// (multitenancy-internals.md §8). A nil details is omitted.
func WriteDetails(ctx context.Context, w http.ResponseWriter, status int, code, message string, details any) {
	env := Envelope{Error: Body{
		Code:      code,
		Message:   message,
		Details:   details,
		RequestID: RequestIDFromContext(ctx),
		TraceID:   TraceIDFromContext(ctx),
	}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := encode(w, env); err != nil {
		log.Error().Err(err).Msg("httperr: encode error response")
	}
}

// moduleEnvelope keeps every member of a module's error body as raw
// bytes so Annotate changes only the two ids. The ids decode into any so a
// module-sent value of any JSON type is still overwritten.
type moduleEnvelope struct {
	Error   *moduleBody    `json:"error"`
	Unknown jsontext.Value `json:",embed"`
}

type moduleBody struct {
	Code      jsontext.Value `json:"code,omitzero"`
	Message   jsontext.Value `json:"message,omitzero"`
	Details   jsontext.Value `json:"details,omitzero"`
	RequestID any            `json:"request_id,omitempty"`
	TraceID   any            `json:"trace_id,omitempty"`
	Unknown   jsontext.Value `json:",embed"`
}

// Annotate sets request_id/trace_id on an error envelope a module
// produced (a WASM handler's or an EnableOps route's response body),
// overwriting whatever the module itself put there — the engine's ids are
// the only ones an operator can look up. A body that isn't an error
// envelope comes back unchanged.
func Annotate(ctx context.Context, body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var env moduleEnvelope
	if err := json.Unmarshal(body, &env, jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true)); err != nil {
		log.Warn().Err(err).Msg("httperr: module error body is not JSON; ids not added")
		return body
	}
	if env.Error == nil {
		return body
	}
	env.Error.RequestID = RequestIDFromContext(ctx)
	env.Error.TraceID = TraceIDFromContext(ctx)
	out, err := json.Marshal(env, jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true), jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		log.Warn().Err(err).Msg("httperr: re-encode module error body; ids not added")
		return body
	}
	return out
}

// encode matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own: map keys sorted, '<', '>',
// '&' escaped for safe HTML embedding, and U+2028/U+2029 escaped for safe
// JS embedding.
func encode(w http.ResponseWriter, v any) error {
	return json.MarshalWrite(w, v, json.Deterministic(true), jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}
