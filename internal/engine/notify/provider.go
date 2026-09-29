package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

// openStatuses are the delivery statuses a provider job still sends to.
// Every other status is final: the delivery was sent, reported on by the
// provider, or given up on.
var openStatuses = []string{notifications.DeliveryPending, notifications.DeliveryRetrying, notifications.DeliveryQuotaExceeded}

// ProviderDeliveries tracks the sms_send and push_send jobs the pipeline
// enqueues (notification-system.md §4) on their notification's
// notification_deliveries rows, around the connector's handle_job:
// Begin keeps a retried job from sending to a delivery that is already
// final (§3 "Provider-neutral idempotency contract"), and Finish records
// each attempt's outcome. It satisfies jobdispatch.DeliveryTracker.
type ProviderDeliveries struct {
	DB      *sql.DB
	Tenants TenantLookup
}

// Begin returns the payload args' handler runs with: args' own, with a
// push_send's tokens narrowed to the deliveries still open. ok is false
// when no delivery is open, because every one is final or the
// notification is gone, and the job has nothing left to send.
func (p *ProviderDeliveries) Begin(ctx context.Context, args jobqueue.WASMJobArgs) (payload []byte, ok bool, err error) {
	channel, err := deliveryChannel(args.JobType)
	if err != nil {
		return nil, false, err
	}
	schema, err := p.schema(ctx, args.TenantID)
	if err != nil {
		return nil, false, err
	}
	rows, err := p.DB.QueryContext(ctx, fmt.Sprintf(`
		SELECT recipient FROM %s.notification_deliveries
		WHERE notification_id = $1 AND channel = $2 AND status = ANY($3)
	`, schema), args.NotificationID, channel, openStatuses)
	if err != nil {
		return nil, false, fmt.Errorf("load %s deliveries: %w", channel, err)
	}
	defer rows.Close()
	var open []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, false, fmt.Errorf("load %s deliveries: %w", channel, err)
		}
		open = append(open, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("load %s deliveries: %w", channel, err)
	}

	if len(open) == 0 {
		log.Info().Str("notification_id", args.NotificationID).Str("channel", channel).
			Msg("provider delivery: no open delivery left, not sending")
		return nil, false, nil
	}
	if channel != notifications.ChannelPush {
		return args.Payload, true, nil
	}

	var push abiv1.PushSendPayload
	if err := msgpack.Unmarshal(args.Payload, &push); err != nil {
		return nil, false, fmt.Errorf("decode push_send payload: %w", err)
	}
	all := len(push.Tokens)
	push.Tokens = slices.DeleteFunc(push.Tokens, func(tok abiv1.PushDeviceToken) bool { return !slices.Contains(open, tok.Token) })
	switch len(push.Tokens) {
	case 0:
		return nil, false, nil
	case all:
		return args.Payload, true, nil
	}
	narrowed, err := msgpack.Marshal(push)
	if err != nil {
		return nil, false, fmt.Errorf("encode push_send payload: %w", err)
	}
	return narrowed, true, nil
}

// Finish records job's attempt on its open deliveries and returns workErr,
// the attempt's own result, for River. A handler that returned nil leaves
// them accepted: the provider took the send, and only its delivery report
// can say more. A permanent failure, or any failure on the last attempt,
// leaves them failed; any other leaves them retrying. A delivery the
// connector already reported final during the attempt keeps its status.
func (p *ProviderDeliveries) Finish(ctx context.Context, job *river.Job[jobqueue.WASMJobArgs], workErr error) error {
	args := job.Args
	channel, err := deliveryChannel(args.JobType)
	if err != nil {
		return errors.Join(workErr, err)
	}

	status, from, reason := notifications.DeliveryAccepted, openStatuses, ""
	if workErr != nil {
		reason = workErr.Error()
		_, cancelled := errors.AsType[*river.JobCancelError](workErr)
		if cancelled || job.Attempt >= job.MaxAttempts {
			status = notifications.DeliveryFailed
		} else {
			// A quota refusal the connector reported stays quota_exceeded
			// until the retry settles it.
			status, from = notifications.DeliveryRetrying, []string{notifications.DeliveryPending, notifications.DeliveryRetrying}
		}
	}

	// Finish runs after handle_job, whose context may be done by now.
	ctx = context.WithoutCancel(ctx)
	schema, err := p.schema(ctx, args.TenantID)
	if err != nil {
		return errors.Join(workErr, err)
	}
	res, err := p.DB.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s.notification_deliveries SET
			status         = $3,
			attempted_at   = NOW(),
			attempt_count  = $4,
			failure_reason = NULLIF($5, '')
		WHERE notification_id = $1 AND channel = $2 AND status = ANY($6)
	`, schema), args.NotificationID, channel, status, job.Attempt, reason, from)
	if err != nil {
		return errors.Join(workErr, fmt.Errorf("record %s delivery attempt: %w", channel, err))
	}
	n, _ := res.RowsAffected()

	event := log.Info()
	if workErr != nil {
		event = log.Warn().Err(workErr)
	}
	event.Str("tenant_id", args.TenantID).
		Str("notification_id", args.NotificationID).
		Str("channel", channel).
		Str("provider", args.ModuleName).
		Str("status", status).
		Int64("deliveries", n).
		Int("attempt", job.Attempt).
		Msg("provider delivery attempt recorded")
	return workErr
}

func (p *ProviderDeliveries) schema(ctx context.Context, tenantID string) (string, error) {
	t, err := p.Tenants.GetByID(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("load tenant %s: %w", tenantID, err)
	}
	return tenantschema.Name(t.Slug), nil
}

// deliveryChannel is the channel a provider delivery job type delivers on.
func deliveryChannel(jobType string) (string, error) {
	switch jobType {
	case JobTypeSMSSend:
		return notifications.ChannelSMS, nil
	case JobTypePushSend:
		return notifications.ChannelPush, nil
	default:
		return "", fmt.Errorf("%q is not a notification delivery job type", jobType)
	}
}
