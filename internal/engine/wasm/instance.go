package wasm

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

type ModuleInstance struct {
	module        api.Module
	memory        api.Memory
	allocate      api.Function
	deallocate    api.Function
	handleRequest api.Function
	handleEvent   api.Function
	handleJob     api.Function
	moduleCtx     *ModuleContext
	inUse         atomic.Bool
}

// newModuleInstance instantiates compiled under the given (already unique)
// name and wires up its exports — the construction InstancePool.instantiate
// and Runtime.InstantiateTemp both need, so a pooled instance and a
// one-off temporary instance (used for load-time export calls) never
// diverge in what's wired up or whether init runs.
func newModuleInstance(ctx context.Context, name string, compiled wazero.CompiledModule, rt wazero.Runtime) (*ModuleInstance, error) {
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(name))
	if err != nil {
		return nil, fmt.Errorf("instantiate %s: %w", name, err)
	}

	inst := &ModuleInstance{
		module: mod,
		memory: mod.Memory(),
	}
	inst.allocate = mod.ExportedFunction("allocate")
	inst.deallocate = mod.ExportedFunction("deallocate")
	inst.handleRequest = mod.ExportedFunction("handle_request")
	inst.handleEvent = mod.ExportedFunction("handle_event")
	inst.handleJob = mod.ExportedFunction("handle_job")

	if initFn := mod.ExportedFunction("init"); initFn != nil {
		if _, err := initFn.Call(ctx); err != nil {
			_ = mod.CloseWithExitCode(context.Background(), 0)
			return nil, fmt.Errorf("init() for %s: %w", name, err)
		}
	}

	return inst, nil
}

func (inst *ModuleInstance) SetModuleContext(mc *ModuleContext) {
	inst.moduleCtx = mc
}

func (inst *ModuleInstance) ModuleContext() *ModuleContext {
	return inst.moduleCtx
}

func (inst *ModuleInstance) Module() api.Module {
	return inst.module
}

func (inst *ModuleInstance) InvokeNoArg(ctx context.Context, fnName string) ([]byte, error) {
	fn := inst.module.ExportedFunction(fnName)
	if fn == nil {
		return nil, fmt.Errorf("module missing %s export", fnName)
	}
	if inst.deallocate == nil {
		return nil, fmt.Errorf("module missing deallocate export")
	}
	result, err := fn.Call(ctx)
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", fnName, err)
	}

	raw := result[0]
	ptr, length := uint32(raw>>32), uint32(raw)
	view, ok := inst.memory.Read(ptr, length)
	if !ok {
		return nil, fmt.Errorf("could not read %s response at ptr=%d len=%d", fnName, ptr, length)
	}
	data := make([]byte, len(view))
	copy(data, view)

	if _, err := inst.deallocate.Call(context.Background(), uint64(ptr), uint64(length)); err != nil {
		log.Warn().Err(err).Msg("could not deallocate response buffer")
	}

	return data, nil
}

func (inst *ModuleInstance) InvokeHandleRequest(ctx context.Context, payload []byte) ([]byte, error) {
	if inst.allocate == nil {
		return nil, fmt.Errorf("module missing allocate export")
	}
	if inst.handleRequest == nil {
		return nil, fmt.Errorf("module missing handle_request export")
	}
	if inst.deallocate == nil {
		return nil, fmt.Errorf("module missing deallocate export")
	}

	allocResult, err := inst.allocate.Call(ctx, uint64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("allocate %d bytes: %w", len(payload), err)
	}
	if allocResult[0] == 0 {
		return nil, abi.ErrAllocationFailed
	}
	reqPtr := uint32(allocResult[0])

	defer func() {
		if _, err := inst.deallocate.Call(context.Background(), uint64(reqPtr), uint64(len(payload))); err != nil {
			log.Warn().Err(err).Msg("could not deallocate request buffer")
		}
	}()

	if !inst.memory.Write(reqPtr, payload) {
		return nil, fmt.Errorf("memory.Write out of bounds at ptr=%d len=%d", reqPtr, len(payload))
	}

	results, err := inst.handleRequest.Call(ctx, uint64(reqPtr), uint64(len(payload)))
	if err != nil {
		return nil, err
	}

	raw := results[0]
	respPtr := uint32(raw >> 32)
	respLen := uint32(raw)

	view, ok := inst.memory.Read(respPtr, respLen)
	if !ok {
		return nil, fmt.Errorf("could not read response at ptr=%d len=%d", respPtr, respLen)
	}

	data := make([]byte, len(view))
	copy(data, view)

	if _, err := inst.deallocate.Call(context.Background(), uint64(respPtr), uint64(respLen)); err != nil {
		log.Warn().Err(err).Msg("could not deallocate response buffer")
	}

	return data, nil
}

// InvokeHandleEvent is InvokeHandleRequest's sync WASM invocation wrapper
// for a module's handle_event export instead of handle_request — same
// allocate/write/call/read/deallocate calling convention and the same
// division of responsibility: a trap or context deadline surfaces here as
// a plain error, undistinguished from either. Telling them apart is the
// caller's job — the same ctx.Err() check invokeHandler (engine.go)
// already applies around InvokeHandleRequest, which the event-dispatch
// caller (engine-internals.md §9's invokeEventHandler, not yet built)
// applies the same way around this.
func (inst *ModuleInstance) InvokeHandleEvent(ctx context.Context, payload []byte) ([]byte, error) {
	if inst.allocate == nil {
		return nil, fmt.Errorf("module missing allocate export")
	}
	if inst.handleEvent == nil {
		return nil, fmt.Errorf("module missing handle_event export")
	}
	if inst.deallocate == nil {
		return nil, fmt.Errorf("module missing deallocate export")
	}

	allocResult, err := inst.allocate.Call(ctx, uint64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("allocate %d bytes: %w", len(payload), err)
	}
	if allocResult[0] == 0 {
		return nil, abi.ErrAllocationFailed
	}
	reqPtr := uint32(allocResult[0])

	defer func() {
		if _, err := inst.deallocate.Call(context.Background(), uint64(reqPtr), uint64(len(payload))); err != nil {
			log.Warn().Err(err).Msg("could not deallocate request buffer")
		}
	}()

	if !inst.memory.Write(reqPtr, payload) {
		return nil, fmt.Errorf("memory.Write out of bounds at ptr=%d len=%d", reqPtr, len(payload))
	}

	results, err := inst.handleEvent.Call(ctx, uint64(reqPtr), uint64(len(payload)))
	if err != nil {
		return nil, err
	}

	raw := results[0]
	respPtr := uint32(raw >> 32)
	respLen := uint32(raw)

	view, ok := inst.memory.Read(respPtr, respLen)
	if !ok {
		return nil, fmt.Errorf("could not read response at ptr=%d len=%d", respPtr, respLen)
	}

	data := make([]byte, len(view))
	copy(data, view)

	if _, err := inst.deallocate.Call(context.Background(), uint64(respPtr), uint64(respLen)); err != nil {
		log.Warn().Err(err).Msg("could not deallocate response buffer")
	}

	return data, nil
}
