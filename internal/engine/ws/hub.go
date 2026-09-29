// Package ws implements the engine's in-memory registry of live /_ws
// WebSocket connections and their channel subscriptions, plus the
// Broadcast primitive other engine code uses to push messages to them
// (goerp#616). It owns none of the HTTP upgrade/auth handshake — that's
// internal/engine's own dispatchWSRoute, which authenticates the request
// via the standard middleware chain before ever constructing a Conn here.
//
// This deliberately does not cover module-author-registered WS routes
// (engine.WS(path, handler), backlog #111/#462) — dispatching a WSEvent
// into a WASM handler over an ongoing connection is a separate, unresolved
// design question (the SDK's Handler type is one-shot request/response),
// left for whichever ticket takes that on.
package ws

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// inboundMessage is the wire shape a connected client sends to
// subscribe/unsubscribe from a channel (shell-architecture.md §12).
type inboundMessage struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
}

// outboundEnvelope is the wire shape Broadcast sends to every connection
// subscribed to Channel (shell-architecture.md §12's WebSocketManager
// onmessage handler).
type outboundEnvelope struct {
	Channel string `json:"channel"`
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

// Conn is one live connection registered with a Hub.
type Conn struct {
	ID        string
	UserID    string
	TenantID  string
	UserAgent string

	ws *websocket.Conn

	mu       sync.Mutex
	channels map[string]struct{}
}

// Hub is the engine process's single connection/channel registry. The
// zero value is not usable — construct one with NewHub.
type Hub struct {
	mu          sync.RWMutex
	conns       map[string]*Conn
	subscribers map[string]map[string]*Conn // channel -> connID -> Conn
}

func NewHub() *Hub {
	return &Hub{
		conns:       make(map[string]*Conn),
		subscribers: make(map[string]map[string]*Conn),
	}
}

// Serve registers wsConn with the hub, then blocks reading and dispatching
// subscribe/unsubscribe messages until the connection closes or ctx is
// canceled. The caller (dispatchWSRoute) owns accepting/closing wsConn
// itself; Serve only unregisters it on return.
func (h *Hub) Serve(ctx context.Context, wsConn *websocket.Conn, connID, userID, tenantID, userAgent string) error {
	c := &Conn{
		ID:        connID,
		UserID:    userID,
		TenantID:  tenantID,
		UserAgent: userAgent,
		ws:        wsConn,
		channels:  make(map[string]struct{}),
	}

	h.mu.Lock()
	h.conns[connID] = c
	h.mu.Unlock()
	defer h.unregister(c)

	for {
		var msg inboundMessage
		if err := wsjson.Read(ctx, wsConn, &msg); err != nil {
			return err
		}
		switch msg.Type {
		case "subscribe":
			h.subscribe(c, msg.Channel)
		case "unsubscribe":
			h.unsubscribe(c, msg.Channel)
		}
		// An unrecognized message type is silently ignored rather than
		// closing the connection — shell-architecture.md documents only
		// these two inbound message types, but a forward-compatible
		// client sending a type this version doesn't know about yet
		// shouldn't be dropped.
	}
}

func (h *Hub) subscribe(c *Conn, channel string) {
	if channel == "" {
		return
	}

	c.mu.Lock()
	c.channels[channel] = struct{}{}
	c.mu.Unlock()

	h.mu.Lock()
	subs, ok := h.subscribers[channel]
	if !ok {
		subs = make(map[string]*Conn)
		h.subscribers[channel] = subs
	}
	subs[c.ID] = c
	h.mu.Unlock()
}

func (h *Hub) unsubscribe(c *Conn, channel string) {
	c.mu.Lock()
	delete(c.channels, channel)
	c.mu.Unlock()

	h.mu.Lock()
	if subs, ok := h.subscribers[channel]; ok {
		delete(subs, c.ID)
		if len(subs) == 0 {
			delete(h.subscribers, channel)
		}
	}
	h.mu.Unlock()
}

func (h *Hub) unregister(c *Conn) {
	h.mu.Lock()
	delete(h.conns, c.ID)
	h.mu.Unlock()

	c.mu.Lock()
	channels := slices.Collect(maps.Keys(c.channels))
	c.mu.Unlock()

	h.mu.Lock()
	for _, channel := range channels {
		if subs, ok := h.subscribers[channel]; ok {
			delete(subs, c.ID)
			if len(subs) == 0 {
				delete(h.subscribers, channel)
			}
		}
	}
	h.mu.Unlock()
}

// TenantChannel is the well-known channel name for messages meant for every
// currently-connected client belonging to tenantID — the per-tenant analogue
// of ui:user:{id}/ui:role:{role} (shell-architecture.md §12).
func TenantChannel(tenantID string) string {
	return "tenant:" + tenantID
}

// NotificationsChannel is the channel the shell's notification feed
// subscribes to (notification-system.md §9). Every session subscribes to
// it under this one name, so its messages go out with BroadcastUser.
const NotificationsChannel = "notifications"

// broadcastWriteTimeout bounds one connection's write, so a half-open
// connection can't hold a broadcast, and a request that sends one, open.
const broadcastWriteTimeout = 5 * time.Second

// UserChannel is shell-architecture.md §12's own ui:user:{id} channel —
// every currently-connected client belonging to userID, regardless of
// tenant. Used by internal/engine/auth/roleassign's role-change broadcast
// (goerp#619), sending a "role.changed" message type alongside whatever
// "ui.push" messages the shell's own subscribeUIChannels already puts on
// this same channel — a channel can carry more than one message type
// (shell-architecture.md §12's own "notifications" channel example mixes
// notification.new/notification.read_all the same way), each consumer
// filtering by type. The frontend subscriber for "role.changed" itself is
// tracked separately (goerp#624), the same split goerp#621/#614 used for
// the per-tenant module-install broadcast.
func UserChannel(userID string) string {
	return "ui:user:" + userID
}

// Broadcast sends {channel, type, payload} to every connection currently
// subscribed to channel, returning the count actually reached. Writes fan
// out concurrently so one slow or stalled connection can't hold up
// delivery to the rest — a send failure to one connection doesn't stop
// delivery to others either; that connection's own Serve loop will
// observe the same failure on its next read and unregister itself.
func (h *Hub) Broadcast(ctx context.Context, channel, msgType string, payload any) (int, error) {
	return h.broadcast(ctx, channel, msgType, payload, func(*Conn) bool { return true })
}

// BroadcastUser is Broadcast restricted to the channel's subscribers
// connected as userID under tenantID, for a message carrying one user's
// data in one tenant on a channel every session subscribes to under the
// same name, such as NotificationsChannel.
func (h *Hub) BroadcastUser(ctx context.Context, channel, tenantID, userID, msgType string, payload any) (int, error) {
	return h.broadcast(ctx, channel, msgType, payload, func(c *Conn) bool { return c.TenantID == tenantID && c.UserID == userID })
}

func (h *Hub) broadcast(ctx context.Context, channel, msgType string, payload any, include func(*Conn) bool) (int, error) {
	h.mu.RLock()
	var targets []*Conn
	for _, c := range h.subscribers[channel] {
		if include(c) {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()

	env := outboundEnvelope{Channel: channel, Type: msgType, Payload: payload}
	var reached atomic.Int64
	var wg sync.WaitGroup
	for _, c := range targets {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, broadcastWriteTimeout)
			defer cancel()
			if err := wsjson.Write(ctx, c.ws, env); err == nil {
				reached.Add(1)
			}
		})
	}
	wg.Wait()

	count := int(reached.Load())
	if count == 0 && len(targets) > 0 {
		return 0, fmt.Errorf("broadcast to channel %q: reached none of %d subscriber(s)", channel, len(targets))
	}
	return count, nil
}

// Close closes every currently registered connection with status 1001
// ("going away"), or until ctx is done — whichever comes first. Call it
// during engine shutdown so open connections and their Serve goroutines
// don't outlive the rest of the engine's graceful shutdown.
func (h *Hub) Close(ctx context.Context) {
	h.mu.RLock()
	conns := slices.Collect(maps.Values(h.conns))
	h.mu.RUnlock()

	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Go(func() {
			_ = c.ws.Close(websocket.StatusGoingAway, "server shutting down")
		})
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
