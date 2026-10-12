package loader

import (
	"context"
	"fmt"
	"slices"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

func validateHookHandlers(ctx context.Context, mf *manifest.Manifest, inst *wasm.ModuleInstance) error {
	for _, hook := range mf.Hooks {
		if !abiv1.EngineHook(hook.Hook) {
			continue
		}

		fn := inst.Module().ExportedFunction("handle_hook")
		if fn == nil || !slices.Equal(fn.Definition().ParamTypes(), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}) || !slices.Equal(fn.Definition().ResultTypes(), []api.ValueType{api.ValueTypeI64}) {
			return fmt.Errorf("hooks: handle_hook must export (i32, i32) -> i64")
		}

		for _, export := range []struct {
			name    string
			params  []api.ValueType
			results []api.ValueType
		}{
			{name: "allocate", params: []api.ValueType{api.ValueTypeI32}, results: []api.ValueType{api.ValueTypeI32}},
			{name: "deallocate", params: []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}},
		} {
			fn := inst.Module().ExportedFunction(export.name)
			if fn == nil || !slices.Equal(fn.Definition().ParamTypes(), export.params) || !slices.Equal(fn.Definition().ResultTypes(), export.results) {
				return fmt.Errorf("hooks: invalid %s export", export.name)
			}
		}
		if inst.Module().Memory() == nil {
			return fmt.Errorf("hooks: module has no linear memory")
		}

		payload, err := msgpack.Marshal(abiv1.HookRequest{Hook: hook.Hook, Handler: hook.Handler, Validate: true})
		if err != nil {
			return fmt.Errorf("marshal hook validation: %w", err)
		}
		data, err := inst.InvokeHandleHook(ctx, payload)
		if err != nil {
			return fmt.Errorf("validate hook %q handler %q: %w", hook.Hook, hook.Handler, err)
		}

		var result abiv1.HookResult
		if err := msgpack.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("decode hook validation: %w", err)
		}
		if result.Error != "" || result.Request != nil || result.Response != nil {
			return fmt.Errorf("hooks: handler %q validation failed: %s", hook.Handler, result.Error)
		}
	}

	return nil
}
