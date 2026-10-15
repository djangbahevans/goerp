package ws

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"uuid"

	"github.com/rs/zerolog/log"
)

type countedBroadcastBus interface {
	BroadcastBus
	PublishCount(context.Context, string, string) (int, error)
}

type broadcastReceipt struct {
	Instance string `json:"instance"`
	Count    int    `json:"count"`
	Failed   bool   `json:"failed,omitzero"`
}

// BroadcastCounted waits for WebSocket writes on all Redis subscribers and counts
// connections once per instance. An error can follow partial delivery; callers
// must not retry automatically. Redis publication failure falls back locally.
func (h *Hub) BroadcastCounted(ctx context.Context, channel, tenantID, msgType string, payload any) (int, error) {
	if tenantID == "" || channel == NotificationsChannel {
		return 0, fmt.Errorf("counted broadcast: a tenant and a non-notification channel are required")
	}

	h.mu.RLock()
	bus := h.bus
	h.mu.RUnlock()
	if bus == nil {
		return h.broadcastEnvelopes(ctx, channel, msgType, payload, func(c *Conn) bool { return c.TenantID == tenantID }, false)
	}
	counted, ok := bus.(countedBroadcastBus)
	if !ok {
		return 0, fmt.Errorf("counted broadcast: transport does not support recipient counts")
	}

	ctx, cancel := context.WithTimeout(ctx, 2*broadcastWriteTimeout)
	defer cancel()
	reply := "ws:receipts:" + uuid.New().String()
	msgs, closeFn, err := counted.Subscribe(ctx, reply)
	if err != nil {
		log.Warn().Err(err).Msg("subscribe to WebSocket receipts failed; delivering locally")
		return h.broadcastEnvelopes(ctx, channel, msgType, payload, func(c *Conn) bool { return c.TenantID == tenantID }, false)
	}
	defer func() { _ = closeFn() }()

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("encode counted broadcast payload: %w", err)
	}
	raw, err := json.Marshal(broadcastMessage{
		Channel:      channel,
		TenantID:     tenantID,
		Type:         msgType,
		Payload:      rawPayload,
		ReplyChannel: reply,
	})
	if err != nil {
		return 0, fmt.Errorf("encode counted broadcast: %w", err)
	}
	expected, err := counted.PublishCount(ctx, broadcastChannel, string(raw))
	if err != nil {
		log.Warn().Err(err).Msg("publish counted WebSocket broadcast failed; delivering locally")
		return h.broadcastEnvelopes(ctx, channel, msgType, payload, func(c *Conn) bool { return c.TenantID == tenantID }, false)
	}

	if expected == 0 {
		return h.broadcastEnvelopes(ctx, channel, msgType, payload, func(c *Conn) bool { return c.TenantID == tenantID }, false)
	}

	seen := make(map[string]struct{}, expected)
	count := 0
	failed := false
	for len(seen) < expected {
		select {
		case <-ctx.Done():
			return count, fmt.Errorf("await WebSocket receipts: %w", ctx.Err())
		case msg, ok := <-msgs:
			if !ok {
				return count, fmt.Errorf("WebSocket receipt subscription closed")
			}
			var receipt broadcastReceipt
			if err := json.Unmarshal([]byte(msg.Payload), &receipt); err != nil || receipt.Instance == "" || receipt.Count < 0 {
				continue
			}
			if _, duplicate := seen[receipt.Instance]; duplicate {
				continue
			}
			seen[receipt.Instance] = struct{}{}
			count += receipt.Count
			failed = failed || receipt.Failed
		}
	}
	if failed {
		return count, fmt.Errorf("WebSocket delivery failed on an engine instance")
	}
	return count, nil
}

func (h *Hub) acknowledge(ctx context.Context, bus BroadcastBus, channel string, count int, deliveryErr error) {
	raw, err := json.Marshal(broadcastReceipt{
		Instance: h.instanceID,
		Count:    count,
		Failed:   deliveryErr != nil,
	})
	if err == nil {
		err = bus.Publish(ctx, channel, string(raw))
	}
	if err != nil {
		log.Warn().Err(err).Msg("publish WebSocket receipt failed")
	}
}
