package wasm

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

type UIPusher interface {
	BroadcastCounted(ctx context.Context, channel, tenantID, msgType string, payload any) (int, error)
}

func (r *Runtime) SetUIPusher(pusher UIPusher) {
	r.uiPusher = pusher
}

func registerHostUI(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := r.guardedHostModule(rt, "host.ui").
		NewFunctionBuilder().WithFunc(makeUIPush(r)).Export("push").
		Instantiate(ctx)
	return err
}

func makeUIPush(r *Runtime) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		mc := inst.ModuleContext()
		fail := func(err *abiv1.HostError) uint64 {
			return abi.EncodeHostError(ctx, m, inst.allocate, err)
		}
		if !mc.Capabilities().Has(abi.CapUIPush) {
			return fail(abi.CapabilityDenied("ui.push"))
		}

		raw, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return fail(abi.MemoryFault())
		}
		var input abiv1.UIPushInput
		if err := msgpack.Unmarshal(raw, &input); err != nil {
			return fail(abi.DeserializeError(err))
		}
		instructions, hostErr := validateUIPush(input)
		if hostErr != nil {
			return fail(hostErr)
		}
		if r.uiPusher == nil {
			return fail(&abiv1.HostError{
				Code:    abiv1.ErrCodeUnavailable,
				Message: "UI delivery is unavailable",
			})
		}

		count, err := r.uiPusher.BroadcastCounted(ctx, input.Channel, mc.TenantID, "ui.push", map[string]any{"instructions": instructions})
		if err != nil {
			log.Warn().Err(err).Str("tenant_id", mc.TenantID).Msg("push UI instructions failed")
			return fail(&abiv1.HostError{
				Code:    abiv1.ErrCodeUnavailable,
				Message: "UI delivery failed",
			})
		}
		return abi.WriteToModule(ctx, m, inst.allocate, abiv1.UIPushOutput{DeliveredCount: count})
	}
}

func validateUIPush(input abiv1.UIPushInput) ([]map[string]any, *abiv1.HostError) {
	parts := strings.Split(input.Channel, ":")
	valid := len(parts) == 3 && parts[0] == "ui" && (parts[1] == "user" || parts[1] == "role")
	valid = valid || (len(parts) == 4 && parts[0] == "ui" && parts[1] == "record")
	for _, part := range parts {
		valid = valid && part != ""
	}
	if !valid {
		return nil, &abiv1.HostError{
			Code:    abiv1.ErrCodeUIChannelInvalid,
			Message: "invalid UI channel",
		}
	}
	if len(input.Instructions) > 64<<10 {
		return nil, &abiv1.HostError{
			Code:    abiv1.ErrCodeUIPayloadTooLarge,
			Message: "UI instructions exceed 64 KiB",
		}
	}

	instructions := []map[string]any{}
	if err := msgpack.Unmarshal(input.Instructions, &instructions); err != nil {
		return nil, abi.DeserializeError(err)
	}
	if instructions == nil {
		return nil, abi.DeserializeError(errors.New("UI instructions must be an array"))
	}
	for _, instruction := range instructions {
		switch instruction["type"] {
		case "toast", "reload", "set_badge":
		default:
			return nil, &abiv1.HostError{
				Code:    abiv1.ErrCodeUIInstructionNotPushable,
				Message: "only toast, reload and set_badge can be pushed",
			}
		}
	}
	if _, err := json.Marshal(instructions); err != nil {
		return nil, abi.DeserializeError(err)
	}
	return instructions, nil
}
