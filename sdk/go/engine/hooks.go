package engine

import (
	"fmt"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

type (
	HookRequest = abi.HookRequest
	HookResult  = abi.HookResult

	// HookResponse carries a status, headers and JSON-encoded body for an after hook.
	HookResponse = abi.Response
)

// HookHandler receives a lifecycle payload and returns a replacement request or response
// for request hooks. Lifecycle notifications return an empty result; errors are logged.
type HookHandler func(HookRequest) (HookResult, error)

var hookHandlers = map[string]HookHandler{}

// HandleHook registers a manifest handler name during initialization. Empty names, nil
// handlers and duplicate names panic. Each name can serve several hook points.
func HandleHook(name string, handler HookHandler) {
	if name == "" || handler == nil {
		panic("engine.HandleHook: name and handler are required")
	}
	if _, exists := hookHandlers[name]; exists {
		panic("engine.HandleHook: duplicate handler " + name)
	}

	hookHandlers[name] = handler
}

// DispatchHook implements handle_hook. Validation checks registration without executing
// the handler. Decode failures, handler errors and panics return an Error result.
func DispatchHook(ptr, length uint32) uint64 {
	var req HookRequest
	if err := unmarshal(ReadMem(ptr, length), &req); err != nil {
		return writePacked(HookResult{Error: "decode hook request: " + err.Error()})
	}

	return writePacked(runHook(req))
}

func runHook(req HookRequest) (result HookResult) {
	handler, exists := hookHandlers[req.Handler]
	if !exists {
		return HookResult{Error: "hook handler not registered: " + req.Handler}
	}
	if !abi.EngineHook(req.Hook) {
		return HookResult{Error: "unsupported hook: " + req.Hook}
	}
	if req.Validate {
		return HookResult{}
	}

	defer func() {
		if value := recover(); value != nil {
			result = HookResult{Error: fmt.Sprintf("hook handler panicked: %v", value)}
		}
	}()

	result, err := handler(req)
	if err != nil {
		return HookResult{Error: err.Error()}
	}

	return result
}
