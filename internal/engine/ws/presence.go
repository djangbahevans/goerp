package ws

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	presenceRefreshInterval = 30 * time.Second
	presenceLifetime        = 90 * time.Second
	presenceUpdateInterval  = 250 * time.Millisecond
	// Fixed-width UTC timestamps keep Redis's lexical expiry comparisons chronological.
	presenceTimeFormat = "2006-01-02T15:04:05.000000000Z"
)

//go:embed presence_mutate.lua
var presenceMutateScript string

//go:embed presence_publish.lua
var presencePublishScript string

type presenceStore interface {
	Eval(context.Context, string, []string, ...any) (any, error)
}

type PresenceProfile struct {
	Name      string
	AvatarURL *string
}

type presenceUser struct {
	UserID       string    `json:"user_id"`
	Name         string    `json:"name"`
	AvatarURL    *string   `json:"avatar_url"`
	EditingField *string   `json:"editing_field"`
	JoinedAt     time.Time `json:"joined_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

type presenceEntry struct {
	presenceUser
	FocusedAt *time.Time `json:"focused_at"`
}

type presenceKey struct {
	tenantID string
	channel  string
}

func (k presenceKey) keys() []string {
	target, _ := strings.CutPrefix(k.channel, "record:")
	key := "ws:presence:" + k.tenantID + ":" + target
	return []string{key, key + ":updates"}
}

func (h *Hub) joinPresence(ctx context.Context, c *Conn, channel string) (bool, error) {
	c.deliveryMu.Lock()
	defer c.deliveryMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, broadcastWriteTimeout)
	defer cancel()

	if _, _, ok := recordTarget(channel); !ok || !strings.HasPrefix(channel, "record:") || h.presenceStore == nil || c.access.PresenceProfile == nil {
		return false, nil
	}
	if !h.subscribeLocked(ctx, c, channel) {
		h.unsubscribe(c, channel)
		return false, nil
	}

	profile, err := c.access.PresenceProfile(ctx)
	if err != nil {
		return false, fmt.Errorf("resolve presence profile: %w", err)
	}
	now := time.Now().UTC()
	entry := presenceEntry{
		UserID:     c.UserID,
		Name:       profile.Name,
		AvatarURL:  profile.AvatarURL,
		JoinedAt:   now,
		LastSeenAt: now,
	}
	raw, err := json.Marshal(struct {
		presenceEntry
		JoinedAt   string `json:"joined_at"`
		LastSeenAt string `json:"last_seen_at"`
	}{presenceEntry: entry, JoinedAt: now.Format(presenceTimeFormat), LastSeenAt: now.Format(presenceTimeFormat)})
	if err != nil {
		return false, fmt.Errorf("encode presence entry: %w", err)
	}

	key := presenceKey{tenantID: c.TenantID, channel: channel}
	if _, _, _, err := h.mutatePresence(ctx, key, "join", c.ID, string(raw), now); err != nil {
		return false, err
	}
	c.presence[channel] = struct{}{}
	return true, nil
}

func (h *Hub) focusPresence(ctx context.Context, c *Conn, channel string, field *string) error {
	c.deliveryMu.Lock()
	defer c.deliveryMu.Unlock()

	if _, joined := c.presence[channel]; !joined {
		return nil
	}
	now := time.Now().UTC()
	c.focusTimes = slices.DeleteFunc(c.focusTimes, func(sent time.Time) bool {
		return !sent.After(now.Add(-time.Second))
	})
	if len(c.focusTimes) >= 10 {
		return nil
	}
	c.focusTimes = append(c.focusTimes, now)

	raw, err := json.Marshal(field)
	if err != nil {
		return fmt.Errorf("encode presence focus: %w", err)
	}
	_, _, _, err = h.mutatePresence(ctx, presenceKey{tenantID: c.TenantID, channel: channel}, "focus", c.ID, string(raw), now)
	return err
}

func (h *Hub) leavePresence(c *Conn, channel string) {
	if _, joined := c.presence[channel]; !joined {
		return
	}
	delete(c.presence, channel)

	ctx, cancel := context.WithTimeout(context.Background(), broadcastWriteTimeout)
	defer cancel()
	_, _, _, err := h.mutatePresence(ctx, presenceKey{tenantID: c.TenantID, channel: channel}, "leave", c.ID, "", time.Now().UTC())
	if err != nil {
		log.Warn().Err(err).Msg("remove record presence failed")
	}
}

func (h *Hub) mutatePresence(ctx context.Context, key presenceKey, operation, connection, value string, now time.Time) (string, bool, []presenceEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, broadcastWriteTimeout)
	defer cancel()

	result, err := h.presenceStore.Eval(ctx, presenceMutateScript, key.keys(), operation, connection, value,
		now.Format(presenceTimeFormat), now.Add(-presenceLifetime).Format(presenceTimeFormat))
	if err != nil {
		return "", false, nil, fmt.Errorf("update record presence: %w", err)
	}
	values, ok := result.([]any)
	if !ok || len(values) != 3 {
		return "", false, nil, fmt.Errorf("update record presence: invalid script result")
	}
	revision, _ := values[0].(string)
	dirty := values[1] == "1"
	fields, ok := values[2].([]any)
	if !ok || len(fields)%2 != 0 {
		return "", false, nil, fmt.Errorf("read record presence: invalid hash result")
	}
	entries := make([]presenceEntry, 0, len(fields)/2)
	for i := 1; i < len(fields); i += 2 {
		raw, ok := fields[i].(string)
		if !ok {
			return "", false, nil, fmt.Errorf("read record presence: invalid entry")
		}
		var entry presenceEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return "", false, nil, fmt.Errorf("decode record presence: %w", err)
		}
		entries = append(entries, entry)
	}
	if dirty && operation != "snapshot" {
		h.schedulePresence(key)
	}
	return revision, dirty, entries, nil
}

func mergePresence(entries []presenceEntry) []presenceUser {
	slices.SortFunc(entries, func(a, b presenceEntry) int {
		return cmp.Or(a.JoinedAt.Compare(b.JoinedAt), cmp.Compare(a.UserID, b.UserID), cmp.Compare(a.Name, b.Name))
	})
	users := make(map[string]presenceEntry)
	for _, entry := range entries {
		merged, found := users[entry.UserID]
		if !found {
			users[entry.UserID] = entry
			continue
		}
		if entry.LastSeenAt.After(merged.LastSeenAt) {
			merged.LastSeenAt = entry.LastSeenAt
		}
		if entry.FocusedAt != nil && (merged.FocusedAt == nil || entry.FocusedAt.After(*merged.FocusedAt)) {
			merged.FocusedAt = entry.FocusedAt
			merged.EditingField = entry.EditingField
		}
		users[entry.UserID] = merged
	}
	result := make([]presenceUser, 0, len(users))
	for _, entry := range users {
		result = append(result, entry.presenceUser)
	}
	slices.SortFunc(result, func(a, b presenceUser) int {
		return cmp.Or(a.JoinedAt.Compare(b.JoinedAt), cmp.Compare(a.UserID, b.UserID))
	})
	return result
}

func (h *Hub) schedulePresence(key presenceKey) {
	h.presenceMu.Lock()
	defer h.presenceMu.Unlock()

	if h.presenceClosed || h.presenceWindows[key] != nil {
		return
	}
	if h.presenceWindows == nil {
		h.presenceWindows = make(map[presenceKey]*time.Timer)
	}
	h.presenceWG.Add(1)
	h.presenceWindows[key] = time.AfterFunc(presenceUpdateInterval, func() {
		defer h.presenceWG.Done()
		h.presenceMu.Lock()
		delete(h.presenceWindows, key)
		closed := h.presenceClosed
		h.presenceMu.Unlock()
		if closed {
			return
		}
		if err := h.flushPresence(key); err != nil {
			log.Warn().Err(err).Msg("publish record presence failed")
			h.schedulePresence(key)
		}
	})
}

func (h *Hub) flushPresence(key presenceKey) error {
	ctx, cancel := context.WithTimeout(context.Background(), broadcastWriteTimeout)
	defer cancel()

	revision, dirty, entries, err := h.mutatePresence(ctx, key, "snapshot", "", "", time.Now().UTC())
	if err != nil || !dirty {
		return err
	}
	resource, id, _ := recordTarget(key.channel)
	payload, err := json.Marshal(struct {
		Resource string         `json:"resource"`
		RecordID string         `json:"record_id"`
		Users    []presenceUser `json:"users"`
	}{Resource: resource, RecordID: id, Users: mergePresence(entries)})
	if err != nil {
		return fmt.Errorf("encode presence users: %w", err)
	}
	raw, err := json.Marshal(broadcastMessage{Channel: key.channel, TenantID: key.tenantID, Type: "presence.update", Payload: payload})
	if err != nil {
		return fmt.Errorf("encode presence broadcast: %w", err)
	}
	result, err := h.presenceStore.Eval(ctx, presencePublishScript, []string{key.keys()[1]}, revision, broadcastChannel, string(raw))
	if err != nil {
		return fmt.Errorf("publish record presence: %w", err)
	}
	if result == int64(0) {
		h.schedulePresence(key)
	}
	return nil
}

func (h *Hub) refreshPresence(ctx context.Context, now time.Time) {
	h.mu.RLock()
	conns := slices.Collect(maps.Values(h.conns))
	h.mu.RUnlock()

	for _, c := range conns {
		c.deliveryMu.Lock()
		for channel := range c.presence {
			_, _, _, err := h.mutatePresence(ctx, presenceKey{tenantID: c.TenantID, channel: channel}, "refresh", c.ID, "", now.UTC())
			if err != nil {
				log.Warn().Err(err).Msg("refresh record presence failed")
			}
		}
		c.deliveryMu.Unlock()
	}
}

func (h *Hub) runPresence(ctx context.Context) {
	ticks := time.Tick(presenceRefreshInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticks:
			h.refreshPresence(ctx, now)
		}
	}
}

func (h *Hub) stopPresence() {
	h.presenceMu.Lock()
	defer h.presenceMu.Unlock()

	h.presenceClosed = true
	for _, timer := range h.presenceWindows {
		if timer.Stop() {
			h.presenceWG.Done()
		}
	}
	clear(h.presenceWindows)
}
