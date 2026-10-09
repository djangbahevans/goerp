package wasm

import (
	"context"
	"errors"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

func (r *Runtime) readNotifyDeliveryInput[In any](m api.Module, ptr, length uint32) (in In, mc *ModuleContext, hostErr *abiv1.HostError) {
	mc = r.InstanceForModule(m).ModuleContext()
	if !mc.Capabilities().Has(abi.CapNotifyManageDeliveries) {
		return in, mc, abi.CapabilityDenied("notify.manage_deliveries")
	}
	if r.primaryDB == nil {
		return in, mc, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "notification storage is unavailable", Retry: true}
	}

	data, err := abi.ReadFromModule(m.Memory(), ptr, length)
	if err != nil {
		return in, mc, abi.MemoryFault()
	}
	if err := msgpack.Unmarshal(data, &in); err != nil {
		return in, mc, abi.DeserializeError(err)
	}

	return in, mc, nil
}

func makeNotifyRemoveDeviceToken(r *Runtime) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		allocate := r.InstanceForModule(m).allocate
		in, mc, hostErr := r.readNotifyDeliveryInput[abiv1.NotifyRemoveDeviceTokenInput](m, ptr, length)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		qCtx, cancel := context.WithTimeout(ctx, defaultExecTimeout)
		defer cancel()

		if err := notifications.NewStore(r.primaryDB).RemoveDeviceToken(qCtx, mc.TenantSlug, mc.TenantID, in.Token); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, notifyHostError(err))
		}

		return abi.WriteToModule(ctx, m, allocate, struct{}{})
	}
}

func makeNotifyUpdateDeliveryStatus(r *Runtime) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		allocate := r.InstanceForModule(m).allocate
		in, mc, hostErr := r.readNotifyDeliveryInput[abiv1.NotifyUpdateDeliveryStatusInput](m, ptr, length)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		if hostErr := validateDeliveryReport(&in); hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		qCtx, cancel := context.WithTimeout(ctx, defaultExecTimeout)
		defer cancel()

		err := notifications.NewStore(r.primaryDB).UpdateDeliveryStatus(qCtx, mc.TenantSlug, mc.TenantID, in)
		if errors.Is(err, notifications.ErrDeliveryNotFound) {
			return abi.EncodeHostError(ctx, m, allocate, deliveryNotFound())
		}
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, notifyHostError(err))
		}

		return abi.WriteToModule(ctx, m, allocate, struct{}{})
	}
}

func validateDeliveryReport(in *abiv1.NotifyUpdateDeliveryStatusInput) *abiv1.HostError {
	id, err := uuid.Parse(in.NotificationID)
	if err != nil {
		return deliveryNotFound()
	}
	in.NotificationID = id.String()

	if (in.Channel != notifications.ChannelSMS && in.Channel != notifications.ChannelPush) || in.Recipient == "" {
		return &abiv1.HostError{Code: abiv1.ErrCodeNotifyInvalidOptions, Message: "delivery report requires sms or push and a recipient"}
	}
	switch in.Status {
	case notifications.DeliveryAccepted, notifications.DeliveryDelivered, notifications.DeliveryQuotaExceeded, notifications.DeliveryFailed:
		return nil
	default:
		return &abiv1.HostError{Code: abiv1.ErrCodeNotifyInvalidOptions, Message: "delivery status must be accepted, delivered, quota_exceeded or failed"}
	}
}

func deliveryNotFound() *abiv1.HostError {
	return &abiv1.HostError{Code: abiv1.ErrCodeNotifyDeliveryNotFound, Message: "no delivery for this notification, channel and recipient in the calling tenant"}
}
