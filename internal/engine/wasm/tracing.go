package wasm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sync"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type invocationTraceKey struct{}

type moduleSpan struct {
	id   string
	span trace.Span
}

type invocationTrace struct {
	mu     sync.Mutex
	tracer trace.Tracer
	root   trace.Span
	spans  []moduleSpan
}

func beginInvocationTrace(ctx context.Context, tracer trace.Tracer) (context.Context, func()) {
	if tracer == nil {
		tracer = otel.Tracer("goerp.wasm")
	}

	state := &invocationTrace{tracer: tracer, root: trace.SpanFromContext(ctx)}
	return context.WithValue(ctx, invocationTraceKey{}, state), state.close
}

func invocationTraceFromContext(ctx context.Context) *invocationTrace {
	state, _ := ctx.Value(invocationTraceKey{}).(*invocationTrace)
	return state
}

func (s *invocationTrace) current(ctx context.Context) context.Context {
	parent := s.root
	if len(s.spans) > 0 {
		parent = s.spans[len(s.spans)-1].span
	}

	return trace.ContextWithSpan(ctx, parent)
}

func (s *invocationTrace) start(ctx context.Context, name string, attrs []attribute.KeyValue) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, span := s.tracer.Start(s.current(ctx), name, trace.WithAttributes(attrs...))
	id := uuid.New().String()
	s.spans = append(s.spans, moduleSpan{id: id, span: span})
	return id
}

func (s *invocationTrace) end(id, status, message string) *abiv1.HostError {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := slices.IndexFunc(s.spans, func(open moduleSpan) bool { return open.id == id })
	if i < 0 {
		return traceSpanNotFound()
	}

	span := s.spans[i].span
	if message != "" {
		span.RecordError(errors.New(message))
	}
	switch status {
	case "ok":
		span.SetStatus(codes.Ok, "")
	case "error":
		span.SetStatus(codes.Error, message)
	}

	span.End()
	s.spans = slices.Delete(s.spans, i, i+1)
	return nil
}

func (s *invocationTrace) setAttr(id string, attr attribute.KeyValue) *abiv1.HostError {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := slices.IndexFunc(s.spans, func(open moduleSpan) bool { return open.id == id })
	if i < 0 {
		return traceSpanNotFound()
	}

	s.spans[i].span.SetAttributes(attr)
	return nil
}

func (s *invocationTrace) close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, open := range slices.Backward(s.spans) {
		open.span.End()
	}
	clear(s.spans)
	s.spans = nil
}

func (r *Runtime) startHostSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	if state := invocationTraceFromContext(ctx); state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()

		return state.tracer.Start(state.current(ctx), name)
	}

	tracer := r.tracer
	if tracer == nil {
		tracer = otel.Tracer("goerp.wasm")
	}

	return tracer.Start(ctx, name)
}

func traceSpanNotFound() *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeTraceSpanNotFound,
		Message: "span is not open in this invocation",
	}
}

func traceInvalidArgument(err error) *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeTraceInvalidArgument,
		Message: err.Error(),
	}
}

func traceAttribute(key string, value any) (attribute.KeyValue, error) {
	if key == "" || value == nil {
		return attribute.KeyValue{}, fmt.Errorf("attribute requires a non-empty key and scalar value")
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.String:
		return attribute.String(key, v.String()), nil
	case reflect.Bool:
		return attribute.Bool(key, v.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return attribute.Int64(key, v.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.Uint() <= math.MaxInt64 {
			return attribute.Int64(key, int64(v.Uint())), nil
		}
	case reflect.Float32, reflect.Float64:
		if f := v.Float(); !math.IsNaN(f) && !math.IsInf(f, 0) {
			return attribute.Float64(key, f), nil
		}
	}

	return attribute.KeyValue{}, fmt.Errorf("attribute %q requires a string, bool, int64-range integer or finite float", key)
}
