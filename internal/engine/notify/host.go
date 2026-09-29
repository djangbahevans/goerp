package notify

import (
	"context"
	"database/sql"
	"errors"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// HostSender is the Sender as host.notify reaches it (wasm.NotifySender):
// a module's send, with its options in their ABI shape and its caller
// errors reported as host.notify error codes.
type HostSender struct {
	*Sender
}

var _ wasm.NotifySender = HostSender{}

func (h HostSender) SendBulk(ctx context.Context, req wasm.NotifyRequest) ([]abiv1.NotifyRecipientResult, error) {
	results, err := h.Sender.SendBulk(ctx, req.TenantID, req.ModuleName, req.NotificationType, req.UserIDs, req.Data, hostOptions(req))
	if err != nil {
		return nil, hostError(err)
	}
	out := make([]abiv1.NotifyRecipientResult, len(results))
	for i, res := range results {
		out[i] = recipientResult(res)
	}
	return out, nil
}

func (h HostSender) SendTx(ctx context.Context, tx *sql.Tx, req wasm.NotifyRequest) (abiv1.NotifyRecipientResult, func(context.Context), error) {
	var userID string
	if len(req.UserIDs) == 1 {
		userID = req.UserIDs[0]
	}
	res, err := h.Sender.SendTx(ctx, tx, req.TenantID, req.ModuleName, req.NotificationType, userID, req.Data, hostOptions(req))
	if err != nil {
		return abiv1.NotifyRecipientResult{}, nil, hostError(err)
	}
	return recipientResult(res), func(ctx context.Context) { h.Announce(ctx, res) }, nil
}

func hostOptions(req wasm.NotifyRequest) Options {
	opts := Options{
		AdditionalChannels: req.Opts.AdditionalChannels,
		ActionURL:          req.Opts.ActionURL,
		Priority:           req.Opts.Priority,
		TraceID:            req.TraceID,
		IdempotencyKey:     req.Opts.IdempotencyKey,
	}
	if req.Opts.ChannelOverride != "" {
		opts.ForceChannels = []string{req.Opts.ChannelOverride}
	}
	return opts
}

func recipientResult(res *Result) abiv1.NotifyRecipientResult {
	return abiv1.NotifyRecipientResult{
		UserID: res.UserID, NotificationID: res.NotificationID, ChannelsUsed: res.ChannelsUsed, Deduplicated: res.Deduplicated,
	}
}

// hostError reports a caller error as its host.notify error code, and
// leaves any other error as it is.
func hostError(err error) error {
	var code string
	switch {
	case errors.Is(err, ErrUndeclaredType):
		code = abiv1.ErrCodeNotifyUndeclaredType
	case errors.Is(err, ErrUnknownUser):
		code = abiv1.ErrCodeNotifyUnknownRecipient
	case errors.Is(err, ErrUnknownChannel), errors.Is(err, ErrInvalidPriority):
		code = abiv1.ErrCodeNotifyInvalidOptions
	case errors.Is(err, ErrTooManyRecipients):
		code = abiv1.ErrCodeNotifyTooManyRecipients
	default:
		return err
	}
	return &abiv1.HostError{Code: code, Message: err.Error()}
}
