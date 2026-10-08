package wasm

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/guestclock"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// ErrNoHandleCron reports a module that does not export handle_cron, so a
// cron job cannot ever run on it.
var ErrNoHandleCron = errors.New("module missing handle_cron export")

type ModuleInstance struct {
	clock               *guestclock.Clock
	module              api.Module
	memory              api.Memory
	allocate            api.Function
	deallocate          api.Function
	handleRequest       api.Function
	handleEvent         api.Function
	handleJob           api.Function
	handleCron          api.Function
	handleActivity      api.Function
	handleVirtualOp     api.Function
	handleCompute       api.Function
	handleCacheLoader   api.Function
	handlePreview       api.Function
	handleConstraint    api.Function
	handleWebhookVerify api.Function
	moduleCtx           *ModuleContext
	inUse               atomic.Bool
}

func newModuleInstance(ctx context.Context, name string, compiled wazero.CompiledModule, rt *Runtime) (*ModuleInstance, error) {
	ctx = guestclock.InitializationContext(ctx)

	// Go WASI reactors initialize through _initialize; commands use _start.
	clock := guestclock.New(ctx, rt.Now)
	mod, err := rt.wazero.InstantiateModule(ctx, compiled, clock.Configure(rt.moduleConfig).WithName(name).WithStartFunctions("_start", "_initialize"))
	if err != nil {
		return nil, fmt.Errorf("instantiate %s: %w", name, err)
	}

	inst := &ModuleInstance{
		clock:  clock,
		module: mod,
		memory: mod.Memory(),
	}
	inst.allocate = mod.ExportedFunction("allocate")
	inst.deallocate = mod.ExportedFunction("deallocate")
	inst.handleRequest = mod.ExportedFunction("handle_request")
	inst.handleEvent = mod.ExportedFunction("handle_event")
	inst.handleJob = mod.ExportedFunction("handle_job")
	inst.handleCron = mod.ExportedFunction("handle_cron")
	inst.handleActivity = mod.ExportedFunction("handle_activity")
	inst.handleVirtualOp = mod.ExportedFunction("handle_virtual_op")
	inst.handleCompute = mod.ExportedFunction("handle_orm_compute")
	inst.handleCacheLoader = mod.ExportedFunction("handle_cache_loader")
	inst.handlePreview = mod.ExportedFunction("handle_orm_preview")
	inst.handleConstraint = mod.ExportedFunction("handle_orm_constraint")
	inst.handleWebhookVerify = mod.ExportedFunction("handle_webhook_verify")

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
	ctx = inst.invocationContext(ctx)

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

func (inst *ModuleInstance) InvokeHandleActivity(ctx context.Context, payload []byte) ([]byte, error) {
	ctx = inst.invocationContext(ctx)

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

func (inst *ModuleInstance) InvokeHandleVirtualOp(ctx context.Context, payload []byte) ([]byte, error) {
	ctx = inst.invocationContext(ctx)

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

func (inst *ModuleInstance) InvokeHandleComputed(ctx context.Context, payload []byte) ([]byte, error) {
	return inst.invokeBufferExport(ctx, inst.handleCompute, "handle_orm_compute", payload)
}

func (inst *ModuleInstance) InvokeHandleCacheLoader(ctx context.Context, payload []byte) ([]byte, error) {
	return inst.invokeBufferExport(ctx, inst.handleCacheLoader, "handle_cache_loader", payload)
}

func (inst *ModuleInstance) HasWebhookVerifier() bool {
	return inst.handleWebhookVerify != nil
}

func (inst *ModuleInstance) InvokeHandleWebhookVerify(ctx context.Context, payload []byte) ([]byte, error) {
	return inst.invokeBufferExport(ctx, inst.handleWebhookVerify, "handle_webhook_verify", payload)
}

// Buffer exports accept (ptr, len) and return a packed response pointer and length.
func (inst *ModuleInstance) invokeBufferExport(ctx context.Context, fn api.Function, exportName string, payload []byte) ([]byte, error) {
	ctx = inst.invocationContext(ctx)

	if inst.allocate == nil {
		return nil, fmt.Errorf("module missing allocate export")
	}
	if fn == nil {
		return nil, fmt.Errorf("module missing %s export", exportName)
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

	results, err := fn.Call(ctx, uint64(reqPtr), uint64(len(payload)))
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

func (inst *ModuleInstance) HasHandlePreview() bool {
	return inst.handlePreview != nil
}

func (inst *ModuleInstance) InvokeHandlePreview(ctx context.Context, payload []byte) ([]byte, error) {
	ctx = inst.invocationContext(ctx)

	if inst.allocate == nil {
		return nil, fmt.Errorf("module missing allocate export")
	}
	if inst.handlePreview == nil {
		return nil, fmt.Errorf("module missing handle_orm_preview export")
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

	results, err := inst.handlePreview.Call(ctx, uint64(reqPtr), uint64(len(payload)))
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

func (inst *ModuleInstance) HasHandleConstraint() bool {
	return inst.handleConstraint != nil
}

func (inst *ModuleInstance) InvokeHandleConstraint(ctx context.Context, payload []byte) ([]byte, error) {
	ctx = inst.invocationContext(ctx)

	if inst.allocate == nil {
		return nil, fmt.Errorf("module missing allocate export")
	}
	if inst.handleConstraint == nil {
		return nil, fmt.Errorf("module missing handle_orm_constraint export")
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

	results, err := inst.handleConstraint.Call(ctx, uint64(reqPtr), uint64(len(payload)))
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

// InvokeHandleEvent returns the handler status: 0 succeeds, 2 fails permanently and any
// other value is retryable. Traps and deadlines return errors; custom RetryAfter delays do
// not cross this ABI.
func (inst *ModuleInstance) InvokeHandleEvent(ctx context.Context, payload []byte) (int32, error) {
	ctx = inst.invocationContext(ctx)

	if inst.allocate == nil {
		return 0, fmt.Errorf("module missing allocate export")
	}
	if inst.handleEvent == nil {
		return 0, fmt.Errorf("module missing handle_event export")
	}
	if inst.deallocate == nil {
		return 0, fmt.Errorf("module missing deallocate export")
	}

	allocResult, err := inst.allocate.Call(ctx, uint64(len(payload)))
	if err != nil {
		return 0, fmt.Errorf("allocate %d bytes: %w", len(payload), err)
	}
	if allocResult[0] == 0 {
		return 0, abi.ErrAllocationFailed
	}
	reqPtr := uint32(allocResult[0])

	defer func() {
		if _, err := inst.deallocate.Call(context.Background(), uint64(reqPtr), uint64(len(payload))); err != nil {
			log.Warn().Err(err).Msg("could not deallocate request buffer")
		}
	}()

	if !inst.memory.Write(reqPtr, payload) {
		return 0, fmt.Errorf("memory.Write out of bounds at ptr=%d len=%d", reqPtr, len(payload))
	}

	results, err := inst.handleEvent.Call(ctx, uint64(reqPtr), uint64(len(payload)))
	if err != nil {
		return 0, err
	}

	return int32(uint32(results[0])), nil
}

// InvokeHandleJob sends a msgpack JobEnvelope and returns the handler status: 0 succeeds,
// 2 fails permanently and any other value is retryable.
func (inst *ModuleInstance) InvokeHandleJob(ctx context.Context, payload []byte) (int32, error) {
	return inst.invokeJobExport(ctx, "handle_job", inst.handleJob, payload)
}

// InvokeHandleCron is InvokeHandleJob's counterpart for a module's
// handle_cron export (manifest-spec.md §26): the same calling convention and
// status codes, with a JobEnvelope whose JobType is the cron job's name.
// A module without the export reports ErrNoHandleCron.
func (inst *ModuleInstance) InvokeHandleCron(ctx context.Context, payload []byte) (int32, error) {
	if inst.handleCron == nil {
		return 0, ErrNoHandleCron
	}
	return inst.invokeJobExport(ctx, "handle_cron", inst.handleCron, payload)
}

func (inst *ModuleInstance) invokeJobExport(ctx context.Context, name string, export api.Function, payload []byte) (int32, error) {
	ctx = inst.invocationContext(ctx)

	if inst.allocate == nil {
		return 0, fmt.Errorf("module missing allocate export")
	}
	if export == nil {
		return 0, fmt.Errorf("module missing %s export", name)
	}
	if inst.deallocate == nil {
		return 0, fmt.Errorf("module missing deallocate export")
	}

	allocResult, err := inst.allocate.Call(ctx, uint64(len(payload)))
	if err != nil {
		return 0, fmt.Errorf("allocate %d bytes: %w", len(payload), err)
	}
	if allocResult[0] == 0 {
		return 0, abi.ErrAllocationFailed
	}
	reqPtr := uint32(allocResult[0])

	defer func() {
		if _, err := inst.deallocate.Call(context.Background(), uint64(reqPtr), uint64(len(payload))); err != nil {
			log.Warn().Err(err).Msg("could not deallocate request buffer")
		}
	}()

	if !inst.memory.Write(reqPtr, payload) {
		return 0, fmt.Errorf("memory.Write out of bounds at ptr=%d len=%d", reqPtr, len(payload))
	}

	results, err := export.Call(ctx, uint64(reqPtr), uint64(len(payload)))
	if err != nil {
		return 0, err
	}

	return int32(uint32(results[0])), nil
}
