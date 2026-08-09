package wasm

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/rs/zerolog/log"
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

func (inst *ModuleInstance) SetModuleContext(mc *ModuleContext) {
	inst.moduleCtx = mc
}

func (inst *ModuleInstance) ModuleContext() *ModuleContext {
	return inst.moduleCtx
}

func (inst *ModuleInstance) Module() api.Module {
	return inst.module
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
