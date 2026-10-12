package wasm

import (
	"context"
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
	"go.opentelemetry.io/otel/attribute"
)

func registerHostTrace(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := r.guardedHostModule(rt, "host.trace").
		NewFunctionBuilder().WithFunc(makeTraceCall[abiv1.TraceSpanStartInput](startModuleSpan)).Export("span_start").
		NewFunctionBuilder().WithFunc(makeTraceCall[abiv1.TraceSpanEndInput](endModuleSpan)).Export("span_end").
		NewFunctionBuilder().WithFunc(makeTraceCall[abiv1.TraceSpanAttrInput](setModuleSpanAttr)).Export("span_attr").
		Instantiate(ctx)
	return err
}

func makeTraceCall[T any](call func(context.Context, *invocationTrace, T) (any, *abiv1.HostError)) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		allocate := m.ExportedFunction("allocate")
		data, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}

		var input T
		if err := msgpack.Unmarshal(data, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		state := invocationTraceFromContext(ctx)
		if state == nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeUnavailable,
				Message: "tracing requires an active invocation",
			})
		}

		output, hostErr := call(ctx, state, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		return abi.WriteToModule(ctx, m, allocate, output)
	}
}

func startModuleSpan(ctx context.Context, state *invocationTrace, input abiv1.TraceSpanStartInput) (any, *abiv1.HostError) {
	if input.Name == "" {
		return nil, traceInvalidArgument(fmt.Errorf("span name must not be empty"))
	}

	attrs := make([]attribute.KeyValue, 0, len(input.Attrs))
	for key, value := range input.Attrs {
		attr, err := traceAttribute(key, value)
		if err != nil {
			return nil, traceInvalidArgument(err)
		}
		attrs = append(attrs, attr)
	}

	return abiv1.TraceSpanStartOutput{SpanID: state.start(ctx, input.Name, attrs)}, nil
}

func endModuleSpan(_ context.Context, state *invocationTrace, input abiv1.TraceSpanEndInput) (any, *abiv1.HostError) {
	switch input.Status {
	case "", "unset", "ok", "error":
	default:
		return nil, traceInvalidArgument(fmt.Errorf("status must be unset, ok or error"))
	}

	return struct{}{}, state.end(input.SpanID, input.Status, input.ErrorMessage)
}

func setModuleSpanAttr(_ context.Context, state *invocationTrace, input abiv1.TraceSpanAttrInput) (any, *abiv1.HostError) {
	attr, err := traceAttribute(input.Key, input.Value)
	if err != nil {
		return nil, traceInvalidArgument(err)
	}

	return struct{}{}, state.setAttr(input.SpanID, attr)
}
