package ws

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/rs/zerolog/log"
)

const broadcastChannel = "ws:broadcast"

type BroadcastBus interface {
	Publish(context.Context, string, string) error
	Subscribe(context.Context, string) (<-chan cache.Message, func() error, error)
}

type broadcastMessage struct {
	Channel  string         `json:"channel"`
	TenantID string         `json:"tenant_id"`
	UserID   string         `json:"user_id,omitempty"`
	Type     string         `json:"type"`
	Payload  jsontext.Value `json:"payload"`
}

// Start attempts a confirmed Redis subscription before returning and retries
// failed subscriptions in the background. Close stops the listener.
func (h *Hub) Start(ctx context.Context, bus BroadcastBus) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.stop != nil {
		return
	}

	ctx, h.stop = context.WithCancel(ctx)
	h.bus = bus
	h.done = make(chan struct{})
	msgs, closeFn, err := bus.Subscribe(ctx, broadcastChannel)
	go h.listen(ctx, bus, msgs, closeFn, err)
}

func (h *Hub) listen(ctx context.Context, bus BroadcastBus, msgs <-chan cache.Message, closeFn func() error, err error) {
	defer close(h.done)

	for ctx.Err() == nil {
		if err != nil {
			log.Warn().Err(err).Msg("subscribe to WebSocket broadcasts failed; retrying")
		} else {
			h.receive(ctx, msgs)
			_ = closeFn()
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
		msgs, closeFn, err = bus.Subscribe(ctx, broadcastChannel)
	}
	if closeFn != nil {
		_ = closeFn()
	}
}

func (h *Hub) receive(ctx context.Context, msgs <-chan cache.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case delivery, ok := <-msgs:
			if !ok {
				return
			}
			var msg broadcastMessage
			if err := json.Unmarshal([]byte(delivery.Payload), &msg); err != nil {
				log.Warn().Err(err).Msg("decode WebSocket broadcast failed")
				continue
			}
			if _, err := h.deliver(ctx, msg); err != nil {
				log.Warn().Err(err).Msg("deliver WebSocket broadcast failed")
			}
		}
	}
}

func (h *Hub) publish(ctx context.Context, channel, tenantID, userID, msgType string, payload any) (int, error) {
	if tenantID == "" {
		return 0, fmt.Errorf("broadcast: a target tenant is required")
	}

	h.mu.RLock()
	bus := h.bus
	h.mu.RUnlock()

	if bus != nil {
		rawPayload, err := json.Marshal(payload)
		if err != nil {
			return 0, fmt.Errorf("encode WebSocket broadcast payload: %w", err)
		}
		msg := broadcastMessage{
			Channel:  channel,
			TenantID: tenantID,
			UserID:   userID,
			Type:     msgType,
			Payload:  rawPayload,
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			return 0, fmt.Errorf("encode WebSocket broadcast: %w", err)
		}

		if err := bus.Publish(ctx, broadcastChannel, string(raw)); err == nil {
			// Redis delivers asynchronously; no local recipient count is available here.
			return 0, nil
		} else {
			log.Warn().Err(err).Str("channel", channel).Msg("publish WebSocket broadcast failed; delivering locally")
		}
	}

	return h.broadcast(ctx, channel, msgType, payload, func(c *Conn) bool {
		return c.TenantID == tenantID && (userID == "" || c.UserID == userID)
	})
}

func (h *Hub) deliver(ctx context.Context, msg broadcastMessage) (int, error) {
	if msg.TenantID == "" || msg.Channel == "" || msg.Type == "" || (msg.Channel == NotificationsChannel && msg.UserID == "") {
		return 0, fmt.Errorf("invalid WebSocket broadcast target or type")
	}

	return h.broadcast(ctx, msg.Channel, msg.Type, msg.Payload, func(c *Conn) bool {
		return c.TenantID == msg.TenantID && (msg.UserID == "" || c.UserID == msg.UserID)
	})
}
