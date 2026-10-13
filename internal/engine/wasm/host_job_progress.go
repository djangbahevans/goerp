package wasm

import (
	"context"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/jobprogress"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

func makeJobsReportProgress(r *Runtime) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		var input abiv1.JobsReportProgressInput
		if err == nil && msgpack.Unmarshal(inputBytes, &input) == nil {
			if inst != nil && inst.ModuleContext() != nil {
				if reporter := inst.ModuleContext().JobProgress; reporter != nil {
					reporter.ReportProgress(input.Percent, input.Message)
				}
			}
		}
		return abi.WriteToModule(ctx, m, m.ExportedFunction("allocate"), struct{}{})
	}
}

func makeJobsReportResult(r *Runtime) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		data, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, m.ExportedFunction("allocate"), abi.MemoryFault())
		}
		var input abiv1.JobsReportResultInput
		if err := msgpack.Unmarshal(data, &input); err != nil {
			return abi.EncodeHostError(ctx, m, m.ExportedFunction("allocate"), abi.DeserializeError(err))
		}
		if err := jobprogress.ValidateResult(input.Value); err != nil {
			return abi.EncodeHostError(ctx, m, m.ExportedFunction("allocate"), err)
		}
		if inst != nil && inst.ModuleContext() != nil {
			if reporter := inst.ModuleContext().JobProgress; reporter != nil {
				if err := reporter.ReportResult(input.Value); err != nil {
					return abi.EncodeHostError(ctx, m, m.ExportedFunction("allocate"), err)
				}
			}
		}
		return abi.WriteToModule(ctx, m, m.ExportedFunction("allocate"), struct{}{})
	}
}
