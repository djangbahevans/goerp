package wasm

import (
	"cmp"
	"context"
	"fmt"
	"math"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/l10n/format"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
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
		}).Export("get_locale").
		NewFunctionBuilder().WithFunc(i18nFormatter(r, func(data []byte, info abiv1.LocaleInfo) (string, error) {
		var input abiv1.FormatNumberInput
		if err := msgpack.Unmarshal(data, &input); err != nil {
			return "", err
		}
		input.Locale = cmp.Or(input.Locale, info.Locale)
		return format.Number(input)
	})).Export("format_number").
		NewFunctionBuilder().WithFunc(i18nFormatter(r, func(data []byte, info abiv1.LocaleInfo) (string, error) {
		var input abiv1.FormatCurrencyInput
		if err := requireI18nInteger(data, "amount_minor"); err != nil {
			return "", err
		}
		if err := msgpack.Unmarshal(data, &input); err != nil {
			return "", err
		}
		input.Locale = cmp.Or(input.Locale, info.Locale)
		return format.Currency(input)
	})).Export("format_currency").
		NewFunctionBuilder().WithFunc(i18nFormatter(r, func(data []byte, info abiv1.LocaleInfo) (string, error) {
		var input abiv1.FormatDateInput
		if err := requireI18nInteger(data, "unix_ms"); err != nil {
			return "", err
		}
		if err := msgpack.Unmarshal(data, &input); err != nil {
			return "", err
		}
		input.Locale = cmp.Or(input.Locale, info.Locale)
		input.Timezone = cmp.Or(input.Timezone, info.Timezone)
		return format.Date(input)
	})).Export("format_date").Instantiate(ctx)
	return err
}

func requireI18nInteger(data []byte, field string) error {
	var input map[string]any
	if err := msgpack.Unmarshal(data, &input); err != nil {
		return err
	}
	switch value := input[field].(type) {
	case int8, int16, int32, int64, uint8, uint16, uint32:
		return nil
	case uint64:
		if value <= math.MaxInt64 {
			return nil
		}
	}
	return fmt.Errorf("%s must be a signed 64-bit integer", field)
}

func i18nFormatter(r *Runtime, render func([]byte, abiv1.LocaleInfo) (string, error)) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		allocate := m.ExportedFunction("allocate")
		if inst == nil || inst.ModuleContext() == nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "formatting requires an invocation"})
		}
		mc := inst.ModuleContext()
		if _, err := r.PrepareInvocationLocale(ctx, mc); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "locale resolution failed"})
		}
		data, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		value, err := render(data, mc.Locale)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}
		return abi.WriteToModule(ctx, m, allocate, value)
	}
}
