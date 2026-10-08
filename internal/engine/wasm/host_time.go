package wasm

import (
	"context"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

func registerHostTime(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := r.guardedHostModule(rt, "host.time").NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, _, _ uint32) uint64 {
			allocate := m.ExportedFunction("allocate")
			now := r.invocationTime(ctx)

			return abi.WriteToModule(ctx, m, allocate, abiv1.TimeNowOutput{
				UnixMs:  now.UnixMilli(),
				ISO8601: now.Format(time.RFC3339Nano),
			})
		}).Export("now").Instantiate(ctx)

	return err
}
