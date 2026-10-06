package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// NotifyRequest is one host.notify send, as the calling module made it.
type NotifyRequest struct {
	TenantID         string
	ModuleName       string
	NotificationType string
	UserIDs          []string
	Data             map[string]any
	Opts             abiv1.NotifySendOptions
	TraceID          string
}

// NotifySender is the notification pipeline host.notify calls into —
// implemented by notify.HostSender and injected via
// Runtime.SetNotifySender, since notify imports registry, which imports
// this package. A caller error (an undeclared type, an unknown recipient,
// bad options) comes back as an *abiv1.HostError; any other error is the
// pipeline's own and treated as transient.
type NotifySender interface {
	// SendBulk sends req to each of its users, committing all of them
	// together, and pushes each new notification to its recipient.
	SendBulk(ctx context.Context, req NotifyRequest) ([]abiv1.NotifyRecipientResult, error)
	// SendTx sends req to its one user on tx, leaving tx as it found it
	// on failure. announce pushes the new notification to its recipient,
	// and must only run once tx commits.
	SendTx(ctx context.Context, tx *sql.Tx, req NotifyRequest) (res abiv1.NotifyRecipientResult, announce func(context.Context), err error)
}

// SetNotifySender wires host.notify to the notification pipeline. Unset,
// host.notify's functions return abi.unavailable.
func (r *Runtime) SetNotifySender(s NotifySender) {
	r.notifySender = s
}

// registerHostNotify attaches host.notify.send, send_tx and send_bulk.
func registerHostNotify(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := r.guardedHostModule(rt, "host.notify").
		NewFunctionBuilder().WithFunc(makeNotifySend(r)).Export("send").
		NewFunctionBuilder().WithFunc(makeNotifySendTx(r)).Export("send_tx").
		NewFunctionBuilder().WithFunc(makeNotifySendBulk(r)).Export("send_bulk").
		Instantiate(ctx)
	return err
}

func makeNotifySend(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		var input abiv1.NotifySendInput
		if hostErr := readNotifyInput(r, modCtx, m, ptr, length, &input); hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		req, hostErr := notifyRequest(modCtx, input.Type, []string{input.UserID}, input.Data, input.Opts)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		results, err := r.notifySender.SendBulk(ctx, req)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, notifyHostError(err))
		}
		if len(results) != 1 {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeUnavailable, Message: fmt.Sprintf("notification pipeline returned %d results for one recipient", len(results)),
			})
		}
		return abi.WriteToModule(ctx, m, allocate, sendOutput(results[0]))
	}
}

func makeNotifySendTx(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		var input abiv1.NotifySendTxInput
		if hostErr := readNotifyInput(r, modCtx, m, ptr, length, &input); hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		tx, ok := modCtx.Transaction(input.TxID)
		if !ok {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeTransactionNotFound, Message: "transaction ID does not exist or has expired",
			})
		}
		req, hostErr := notifyRequest(modCtx, input.Type, []string{input.UserID}, input.Data, input.Opts)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		res, announce, err := r.notifySender.SendTx(ctx, tx, req)
		if err != nil {
			hostErr := notifyHostError(err)
			// A failure such as a serialization error recurs on every retry
			// within tx; only retrying the whole transaction can succeed.
			hostErr.Retry = false
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		if announce != nil {
			modCtx.AfterCommit(input.TxID, announce)
		}
		return abi.WriteToModule(ctx, m, allocate, sendOutput(res))
	}
}

func makeNotifySendBulk(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		var input abiv1.NotifySendBulkInput
		if hostErr := readNotifyInput(r, modCtx, m, ptr, length, &input); hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		req, hostErr := notifyRequest(modCtx, input.Type, input.UserIDs, input.Data, input.Opts)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		results, err := r.notifySender.SendBulk(ctx, req)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, notifyHostError(err))
		}
		return abi.WriteToModule(ctx, m, allocate, abiv1.NotifySendBulkOutput{Notifications: results})
	}
}

// readNotifyInput checks the caller holds notify.send and the pipeline is
// wired, then decodes the call's input into v.
func readNotifyInput(r *Runtime, modCtx *ModuleContext, m api.Module, ptr, length uint32, v any) *abiv1.HostError {
	if !modCtx.Capabilities().Has(abi.CapNotifySend) {
		return abi.CapabilityDenied("notify.send")
	}
	if r.notifySender == nil {
		return &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "the notification pipeline is not available yet (engine still starting up)", Retry: true}
	}
	inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
	if err != nil {
		return abi.MemoryFault()
	}
	if err := msgpack.Unmarshal(inputBytes, v); err != nil {
		return abi.DeserializeError(err)
	}
	return nil
}

// notifyRequest builds the calling module's send, decoding its msgpack
// template data, which must be a map (or absent).
func notifyRequest(modCtx *ModuleContext, notificationType string, userIDs []string, data []byte, opts abiv1.NotifySendOptions) (NotifyRequest, *abiv1.HostError) {
	var vars map[string]any
	if len(data) > 0 {
		if err := msgpack.Unmarshal(data, &vars); err != nil {
			return NotifyRequest{}, abi.DeserializeError(fmt.Errorf("notification data must be a msgpack map: %w", err))
		}
	}
	return NotifyRequest{
		TenantID:         modCtx.TenantID,
		ModuleName:       modCtx.ModuleName,
		NotificationType: notificationType,
		UserIDs:          userIDs,
		Data:             vars,
		Opts:             opts,
		TraceID:          modCtx.TraceID,
	}, nil
}

// notifyHostError is err as host.notify reports it: the pipeline's own
// HostError for a caller error, else a retryable abi.unavailable.
func notifyHostError(err error) *abiv1.HostError {
	if hostErr, ok := errors.AsType[*abiv1.HostError](err); ok {
		return hostErr
	}
	return &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
}

func sendOutput(res abiv1.NotifyRecipientResult) abiv1.NotifySendOutput {
	return abiv1.NotifySendOutput{NotificationID: res.NotificationID, ChannelsUsed: res.ChannelsUsed, Deduplicated: res.Deduplicated}
}
