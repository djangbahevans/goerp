package wasm

import (
	"cmp"
	"context"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/l10n/translation"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

func (r *Runtime) SetTranslationLookup(lookup translation.Lookup) {
	r.translationLookup = lookup
}

func i18nTranslate(r *Runtime) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		allocate := m.ExportedFunction("allocate")
		data, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}

		var input abiv1.TranslationInput
		if err := msgpack.Unmarshal(data, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}
		if input.Count != nil {
			if err := requireI18nInteger(data, "count"); err != nil {
				return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
			}
		}

		inst := r.InstanceForModule(m)
		if inst == nil || inst.ModuleContext() == nil {
			return abi.WriteToModule(ctx, m, allocate, input.Key)
		}

		mc := inst.ModuleContext()
		if input.Locale == "" {
			if _, err := r.PrepareInvocationLocale(ctx, mc); err != nil {
				return abi.WriteToModule(ctx, m, allocate, input.Key)
			}
		}
		input.Locale = cmp.Or(input.Locale, mc.Locale.Locale)
		value := translation.Translate(r.translationLookup, mc.ModuleName, input)
		return abi.WriteToModule(ctx, m, allocate, value)
	}
}
