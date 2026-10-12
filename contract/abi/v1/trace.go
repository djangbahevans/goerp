package abi

// TraceSpanStartInput requires a non-empty name and scalar attributes with
// non-empty keys: strings, booleans, int64-range integers, or finite floats.
type TraceSpanStartInput struct {
	Name  string         `msgpack:"name"`
	Attrs map[string]any `msgpack:"attrs,omitempty"`
}

type TraceSpanStartOutput struct {
	SpanID string `msgpack:"span_id"`
}

// TraceSpanEndInput accepts unset, ok, or error status. ErrorMessage records an
// exception and supplies the description for error status when non-empty.
type TraceSpanEndInput struct {
	SpanID       string `msgpack:"span_id"`
	Status       string `msgpack:"status,omitempty"`
	ErrorMessage string `msgpack:"error_message,omitempty"`
}

type TraceSpanAttrInput struct {
	SpanID string `msgpack:"span_id"`
	Key    string `msgpack:"key"`
	Value  any    `msgpack:"value"`
}

const (
	ErrCodeTraceSpanNotFound    = "trace.span_not_found"
	ErrCodeTraceInvalidArgument = "trace.invalid_argument"
)
