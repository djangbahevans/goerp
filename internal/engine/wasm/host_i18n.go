package wasm

import (
	"context"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

func registerHostI18n(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := r.guardedHostModule(rt, "host.i18n").NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, _, _ uint32) uint64 {
			inst := r.InstanceForModule(m)
			allocate := m.ExportedFunction("allocate")
			if inst == nil || inst.ModuleContext() == nil {
				return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "locale requires an invocation"})
			}

			mc := inst.ModuleContext()
			if _, err := r.PrepareInvocationLocale(ctx, mc); err != nil {
				return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "locale resolution failed"})
			}

			return abi.WriteToModule(ctx, m, allocate, mc.Locale)
		}).Export("get_locale").Instantiate(ctx)
	return err
}
