// Package ws manages WebSocket connections, authorized channel subscriptions and
// cross-instance broadcasts. HTTP upgrade and authentication belong to the engine.
package ws

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type inboundMessage struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
}

type outboundEnvelope struct {
	Channel string `json:"channel"`
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

type RoleChecker func(context.Context, string) (bool, error)

type AccessChecks struct {
	Role          RoleChecker
	ModuleEnabled func(ctx context.Context, module string) (bool, error)
	ReadResource  func(ctx context.Context, module, resource string) (bool, error)
	ReadRecord    func(ctx context.Context, resource, recordID string) (bool, error)
}

type Conn struct {
	ID        string
	UserID    string
	TenantID  string
	UserAgent string

	ws     *websocket.Conn
	access AccessChecks

	channels map[string]struct{}
}

// Hub is the engine process's single connection/channel registry. The
// zero value is not usable — construct one with NewHub.
type Hub struct {
	mu          sync.RWMutex
	conns       map[string]*Conn
	subscribers map[string]map[string]*Conn
	bus         BroadcastBus
	stop        context.CancelFunc
	done        chan struct{}

	changeMu      sync.Mutex
	feedWindows   map[feedKey]*feedWindow
	changesClosed bool
	changeWG      sync.WaitGroup
}

func NewHub() *Hub {
	return &Hub{
		conns:       make(map[string]*Conn),
		subscribers: make(map[string]map[string]*Conn),
	}
}

func (h *Hub) Serve(ctx context.Context, wsConn *websocket.Conn, connID, userID, tenantID, userAgent string, access AccessChecks) error {
	c := &Conn{
		ID:        connID,
		UserID:    userID,
		TenantID:  tenantID,
		UserAgent: userAgent,
		ws:        wsConn,
		access:    access,
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
			if !h.subscribe(ctx, c, msg.Channel) {
				if err := h.denySubscription(ctx, c, msg.Channel); err != nil {
					return err
				}
			}
		case "unsubscribe":
			h.unsubscribe(c, msg.Channel)
		}
	}
}

func (h *Hub) denySubscription(ctx context.Context, c *Conn, channel string) error {
	ctx, cancel := context.WithTimeout(ctx, broadcastWriteTimeout)
	defer cancel()

	return wsjson.Write(ctx, c.ws, outboundEnvelope{
		Channel: channel,
		Type:    "subscription.denied",
		Payload: map[string]string{"code": "ws.subscription_forbidden"},
	})
}

func (c *Conn) allows(ctx context.Context, channel string) bool {
	if c.UserID == "" || c.TenantID == "" {
		return false
	}

	switch channel {
	case NotificationsChannel, TenantChannel(c.TenantID), UserChannel(c.UserID):
		return true
	}

	ctx, cancel := context.WithTimeout(ctx, broadcastWriteTimeout)
	defer cancel()

	if role, ok := strings.CutPrefix(channel, "ui:role:"); ok && role != "" && c.access.Role != nil {
		allowed, err := c.access.Role(ctx, role)
		return err == nil && allowed
	}

	if mod, ok := feedModule(channel); ok && c.access.ModuleEnabled != nil {
		allowed, err := c.access.ModuleEnabled(ctx, mod)
		return err == nil && allowed
	}

	if resource, id, ok := recordTarget(channel); ok && c.access.ReadRecord != nil {
		allowed, err := c.access.ReadRecord(ctx, resource, id)
		return err == nil && allowed
	}

	return false
}

func feedModule(channel string) (string, bool) {
	mod, ok := strings.CutPrefix(channel, "feed:")
	return mod, ok && mod != "" && !strings.Contains(mod, ":")
}

func recordTarget(channel string) (resource, id string, ok bool) {
	target, found := strings.CutPrefix(channel, "record:")
	if !found {
		target, found = strings.CutPrefix(channel, "ui:record:")
	}
	if !found {
		return "", "", false
	}
	resource, id, ok = strings.Cut(target, ":")
	return resource, id, ok && resource != "" && id != "" && !strings.Contains(id, ":")
}

func feedResource(payload any) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	var change struct {
		Resource string `json:"resource"`
	}
	if err := json.Unmarshal(raw, &change); err != nil {
		return ""
	}
	return change.Resource
}

func (h *Hub) subscribe(ctx context.Context, c *Conn, channel string) bool {
	if !c.allows(ctx, channel) {
		return false
	}

	h.mu.Lock()
	c.channels[channel] = struct{}{}
	subs, ok := h.subscribers[channel]
	if !ok {
		subs = make(map[string]*Conn)
		h.subscribers[channel] = subs
	}
	subs[c.ID] = c
	h.mu.Unlock()
	return true
}

func (h *Hub) unsubscribe(c *Conn, channel string) {
	h.mu.Lock()
	delete(c.channels, channel)
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
	defer h.mu.Unlock()

	delete(h.conns, c.ID)
	for channel := range c.channels {
		if subs, ok := h.subscribers[channel]; ok {
			delete(subs, c.ID)
			if len(subs) == 0 {
				delete(h.subscribers, channel)
			}
		}
	}
}

func TenantChannel(tenantID string) string {
	return "tenant:" + tenantID
}

// Notifications share a channel name across users; publishers must use BroadcastUser.
const NotificationsChannel = "notifications"

// broadcastWriteTimeout bounds one connection's write, so a half-open
// connection can't hold a broadcast, and a request that sends one, open.
const broadcastWriteTimeout = 5 * time.Second

func UserChannel(userID string) string {
	return "ui:user:" + userID
}

// BroadcastTenant returns zero after asynchronous Redis publication, or the number
// of connections reached by local delivery. Notifications require BroadcastUser.
func (h *Hub) BroadcastTenant(ctx context.Context, channel, tenantID, msgType string, payload any) (int, error) {
	if channel == NotificationsChannel {
		return 0, fmt.Errorf("broadcast notifications: a target user is required")
	}

	return h.publish(ctx, channel, tenantID, "", msgType, payload)
}

// BroadcastUser returns zero after asynchronous Redis publication, or the number
// of connections reached by local delivery. Both tenant and user IDs are required.
func (h *Hub) BroadcastUser(ctx context.Context, channel, tenantID, userID, msgType string, payload any) (int, error) {
	if userID == "" {
		return 0, fmt.Errorf("broadcast to user: a target user is required")
	}

	return h.publish(ctx, channel, tenantID, userID, msgType, payload)
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
	mod, feed := feedModule(channel)
	resource := ""
	if feed {
		resource = feedResource(payload)
	}

	var reached atomic.Int64
	var attempted atomic.Int64
	var wg sync.WaitGroup
	for _, c := range targets {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, broadcastWriteTimeout)
			defer cancel()

			if !c.allows(ctx, channel) {
				h.unsubscribe(c, channel)
				return
			}

			if feed {
				if resource == "" || c.access.ReadResource == nil {
					return
				}
				allowed, err := c.access.ReadResource(ctx, mod, resource)
				if err != nil || !allowed {
					return
				}
			}

			attempted.Add(1)
			if err := wsjson.Write(ctx, c.ws, env); err == nil {
				reached.Add(1)
			}
		})
	}
	wg.Wait()

	count := int(reached.Load())
	if count == 0 && attempted.Load() > 0 {
		return 0, fmt.Errorf("broadcast to channel %q: reached none of %d subscriber(s)", channel, attempted.Load())
	}
	return count, nil
}

func (h *Hub) Close(ctx context.Context) {
	h.stopChanges()

	h.mu.RLock()
	conns := slices.Collect(maps.Values(h.conns))
	stop, listenerDone := h.stop, h.done
	h.mu.RUnlock()

	if stop != nil {
		stop()
	}

	var wg sync.WaitGroup
	wg.Go(h.changeWG.Wait)
	if listenerDone != nil {
		wg.Go(func() { <-listenerDone })
	}
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
