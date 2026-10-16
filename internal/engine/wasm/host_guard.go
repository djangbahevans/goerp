package wasm

import (
	"context"
	"fmt"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
	"go.opentelemetry.io/otel/codes"
)

// hostFunc is the shape every host function shares: a request at (ptr, length)
// in the module's memory, a packed response out.
type hostFunc = func(ctx context.Context, m api.Module, ptr, length uint32) uint64

// Webhook verifiers may reach only host.crypto, including namespaces that need no capability.
type guardedHostModule struct {
	builder   wazero.HostModuleBuilder
	runtime   *Runtime
	namespace string
}

func (r *Runtime) guardedHostModule(rt wazero.Runtime, name string) guardedHostModule {
	return guardedHostModule{
		builder:   rt.NewHostModuleBuilder(name),
		runtime:   r,
		namespace: name,
	}
}

func (g guardedHostModule) NewFunctionBuilder() guardedHostFunction {
	return guardedHostFunction{
		builder:   g.builder.NewFunctionBuilder(),
		runtime:   g.runtime,
		namespace: g.namespace,
	}
}

func (g guardedHostModule) Instantiate(ctx context.Context) (api.Module, error) {
	return g.builder.Instantiate(ctx)
}

type guardedHostFunction struct {
	builder   wazero.HostFunctionBuilder
	runtime   *Runtime
	namespace string
	fn        hostFunc
}

func (g guardedHostFunction) WithFunc(fn hostFunc) guardedHostFunction {
	g.fn = fn
	return g
}

func (g guardedHostFunction) Export(name string) guardedHostModule {
	r := g.runtime
	g.builder = g.builder.WithFunc(func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		start := time.Now()
		module := ""
		if inst := r.InstanceForModule(m); inst != nil {
			if mc := inst.ModuleContext(); mc != nil {
				module = mc.ModuleName
			}
		}
		ctx = metric.WithModule(ctx, module)
		function := g.namespace + "." + name
		ctx, span := r.startHostSpan(ctx, function)
		defer span.End()
		status := "error"
		defer func() {
			r.metrics.Host(module, function, status, time.Since(start))
		}()
		defer func() {
			if value := recover(); value != nil {
				status = "panic"
				span.RecordError(fmt.Errorf("host call panic: %v", value))
				span.SetStatus(codes.Error, "host call panic")
				panic(value)
			}
		}()

		result := func() uint64 {
			if inst := r.InstanceForModule(m); inst != nil && g.namespace != "host.crypto" {
				if mc := inst.ModuleContext(); mc != nil && mc.webhookVerify {
					return abi.EncodeHostError(ctx, m, inst.allocate, webhookVerifyHostDenied())
				}
			}
			return g.fn(ctx, m, ptr, length)
		}()

		if result == 0 {
			span.SetStatus(codes.Error, "host response allocation failed")
		} else if raw, ok := m.Memory().Read(uint32(result>>32), uint32(result)); ok {
			var envelope struct {
				OK    bool             `msgpack:"ok"`
				Error *abiv1.HostError `msgpack:"error"`
			}
			if err := msgpack.Unmarshal(raw, &envelope); err == nil {
				if envelope.OK {
					status = "success"
				}
				if envelope.Error != nil {
					span.RecordError(envelope.Error)
					span.SetStatus(codes.Error, envelope.Error.Code)
				}
			}
		}

		return result
	})
	return guardedHostModule{
		builder:   g.builder.Export(name),
		runtime:   g.runtime,
		namespace: g.namespace,
	}
}

func webhookVerifyHostDenied() *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeCapabilityDenied,
		Message: "only host.crypto is available while a webhook verifier runs",
	}
}
