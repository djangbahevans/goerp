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
	module          api.Module
	memory          api.Memory
	allocate        api.Function
	deallocate      api.Function
	handleRequest   api.Function
	handleEvent     api.Function
	handleJob       api.Function
	handleActivity  api.Function
	handleVirtualOp api.Function
	handleCompute   api.Function
	moduleCtx       *ModuleContext
	inUse           atomic.Bool
}

// newModuleInstance instantiates compiled under the given (already unique)
// name and wires up its exports — the construction InstancePool.instantiate
// and Runtime.InstantiateTemp both need, so a pooled instance and a
// one-off temporary instance (used for load-time export calls) never
// diverge in what's wired up or whether init runs.
func newModuleInstance(ctx context.Context, name string, compiled wazero.CompiledModule, rt wazero.Runtime) (*ModuleInstance, error) {
	// WithStartFunctions includes "_initialize" alongside wazero's own
	// default ("_start") because a real module compiled with
	// -buildmode=c-shared (required on wasip1 to produce a WASI
	// reactor/library rather than a command — go help buildmode) exports
	// "_initialize", not "_start": Go's wasip1 command-mode "_start" always
	// calls proc_exit after main() returns, which would close the module
	// before any wasmexport function could ever be called. A hand-built
	// test fixture with neither export is unaffected — wazero silently
	// skips any start function that doesn't exist.
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(name).WithStartFunctions("_start", "_initialize"))
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
	inst.handleActivity = mod.ExportedFunction("handle_activity")
	inst.handleVirtualOp = mod.ExportedFunction("handle_virtual_op")
	inst.handleCompute = mod.ExportedFunction("handle_orm_compute")

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

// InvokeHandleActivity is InvokeHandleRequest's sync WASM invocation wrapper
// for a module's handle_activity export (go-sdk-reference.md §21a).
func (inst *ModuleInstance) InvokeHandleActivity(ctx context.Context, payload []byte) ([]byte, error) {
	if inst.allocate == nil {
		return nil, fmt.Errorf("module missing allocate export")
	}
	if inst.handleActivity == nil {
		return nil, fmt.Errorf("module missing handle_activity export")
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

	results, err := inst.handleActivity.Call(ctx, uint64(reqPtr), uint64(len(payload)))
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

// InvokeHandleVirtualOp is InvokeHandleActivity's sync WASM invocation
// wrapper for a module's handle_virtual_op export — the entry point
// sdk/go/orm.DispatchVirtualOp exports for a Virtual-backed model's
// registered backend function (go-sdk-reference.md §22 "Virtual models").
// A module with no Virtual-backed models never exports handle_virtual_op
// at all; the nil-check below surfaces that as a descriptive error rather
// than a panic, the same way every other Invoke* method here handles a
// missing export.
func (inst *ModuleInstance) InvokeHandleVirtualOp(ctx context.Context, payload []byte) ([]byte, error) {
	if inst.allocate == nil {
		return nil, fmt.Errorf("module missing allocate export")
	}
	if inst.handleVirtualOp == nil {
		return nil, fmt.Errorf("module missing handle_virtual_op export")
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

	results, err := inst.handleVirtualOp.Call(ctx, uint64(reqPtr), uint64(len(payload)))
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

// InvokeHandleComputed is InvokeHandleActivity's sync WASM invocation
// wrapper for a module's handle_orm_compute export — the entry point
// sdk/go/orm.DispatchComputed exports for a .Computed(fnName) field's
// registered compute function (go-sdk-reference.md §22 "Computed field
// recomputation"). A module with no Computed fields declared never
// exports handle_orm_compute at all; the nil-check below surfaces that as
// a descriptive error rather than a panic, the same way every other
// Invoke* method here handles a missing export.
func (inst *ModuleInstance) InvokeHandleComputed(ctx context.Context, payload []byte) ([]byte, error) {
	if inst.allocate == nil {
		return nil, fmt.Errorf("module missing allocate export")
	}
	if inst.handleCompute == nil {
		return nil, fmt.Errorf("module missing handle_orm_compute export")
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

	results, err := inst.handleCompute.Call(ctx, uint64(reqPtr), uint64(len(payload)))
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
