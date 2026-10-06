package wasm

import (
	"context"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// hostFunc is the shape every host function shares: a request at (ptr, length)
// in the module's memory, a packed response out.
type hostFunc = func(ctx context.Context, m api.Module, ptr, length uint32) uint64

// guardedHostModule builds a host namespace whose functions refuse every call
// made while a webhook verifier runs (ModuleContext.webhookVerify). Every
// namespace except host.crypto is built through it, so a verifier cannot reach
// configuration, data or the network.
type guardedHostModule struct {
	builder wazero.HostModuleBuilder
	runtime *Runtime
}

func (r *Runtime) guardedHostModule(rt wazero.Runtime, name string) guardedHostModule {
	return guardedHostModule{builder: rt.NewHostModuleBuilder(name), runtime: r}
}

func (g guardedHostModule) NewFunctionBuilder() guardedHostFunction {
	return guardedHostFunction{builder: g.builder.NewFunctionBuilder(), runtime: g.runtime}
}

func (g guardedHostModule) Instantiate(ctx context.Context) (api.Module, error) {
	return g.builder.Instantiate(ctx)
}

type guardedHostFunction struct {
	builder wazero.HostFunctionBuilder
	runtime *Runtime
}

func (g guardedHostFunction) WithFunc(fn hostFunc) guardedHostFunction {
	r := g.runtime
	g.builder = g.builder.WithFunc(func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		if inst := r.InstanceForModule(m); inst != nil {
			if mc := inst.ModuleContext(); mc != nil && mc.webhookVerify {
				return abi.EncodeHostError(ctx, m, inst.allocate, webhookVerifyHostDenied())
			}
		}
		return fn(ctx, m, ptr, length)
	})
	return g
}

func (g guardedHostFunction) Export(name string) guardedHostModule {
	return guardedHostModule{builder: g.builder.Export(name), runtime: g.runtime}
}

func webhookVerifyHostDenied() *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeCapabilityDenied,
		Message: "only host.crypto is available while a webhook verifier runs",
	}
}
