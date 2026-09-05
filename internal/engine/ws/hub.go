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
	"sync"

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
	channels := make([]string, 0, len(c.channels))
	for channel := range c.channels {
		channels = append(channels, channel)
	}
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

// Broadcast sends {channel, type, payload} to every connection currently
// subscribed to channel, returning the count actually reached. A send
// failure to one connection doesn't stop delivery to the rest — that
// connection's own Serve loop will observe the same failure on its next
// read and unregister itself.
func (h *Hub) Broadcast(ctx context.Context, channel, msgType string, payload any) (int, error) {
	h.mu.RLock()
	subs := h.subscribers[channel]
	targets := make([]*Conn, 0, len(subs))
	for _, c := range subs {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	env := outboundEnvelope{Channel: channel, Type: msgType, Payload: payload}
	reached := 0
	for _, c := range targets {
		if err := wsjson.Write(ctx, c.ws, env); err != nil {
			continue
		}
		reached++
	}
	if reached == 0 && len(targets) > 0 {
		return 0, fmt.Errorf("broadcast to channel %q: reached none of %d subscriber(s)", channel, len(targets))
	}
	return reached, nil
}
