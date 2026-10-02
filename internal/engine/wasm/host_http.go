package wasm

import (
	"context"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

func registerHostHTTP(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := rt.NewHostModuleBuilder("host.http").
		NewFunctionBuilder().WithFunc(makeHTTPFetch(r)).Export("fetch").
		Instantiate(ctx)

	return err
}

func makeHTTPFetch(r *Runtime) func(context.Context, api.Module, uint32, uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		if inst == nil || inst.ModuleContext() == nil {
			return abi.EncodeHostError(ctx, m, m.ExportedFunction("allocate"), &abiv1.HostError{
				Code: abiv1.ErrCodeUnavailable, Message: "module execution context is not available",
			})
		}

		mc := inst.ModuleContext()

		if !mc.Capabilities().Has(abi.CapHTTPFetch) {
			return abi.EncodeHostError(ctx, m, inst.allocate, abi.CapabilityDenied("http.fetch"))
		}

		raw, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, inst.allocate, abi.MemoryFault())
		}

		var input abiv1.HTTPFetchInput

		if err := msgpack.Unmarshal(raw, &input); err != nil {
			return abi.EncodeHostError(ctx, m, inst.allocate, abi.DeserializeError(err))
		}

		out, hostErr := r.httpFetcher.fetch(ctx, mc, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, inst.allocate, hostErr)
		}

		return abi.WriteToModule(ctx, m, inst.allocate, out)
	}
}
