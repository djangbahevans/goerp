package ws

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

type feedKey struct {
	tenantID string
	resource string
}

type feedWindow struct {
	timer *time.Timer
	dirty bool
}

// RecordChanged publishes identifier-only invalidations for a committed ORM write.
// Creates invalidate the feed; updates and deletes also notify the record channel.
func (h *Hub) RecordChanged(ctx context.Context, tenantID, resource, recordID, operation string) {
	module, _, ok := strings.Cut(resource, ".")
	if !ok || module == "" || tenantID == "" {
		return
	}

	h.changeMu.Lock()
	if h.changesClosed {
		h.changeMu.Unlock()
		return
	}
	h.changeWG.Add(1)
	h.changeMu.Unlock()
	defer h.changeWG.Done()

	// A committed change remains relevant when its initiating request is canceled.
	ctx = context.WithoutCancel(ctx)
	h.feedChanged(ctx, feedKey{tenantID: tenantID, resource: resource}, module)
	if recordID != "" && (operation == "updated" || operation == "deleted") {
		h.publishChange(ctx, "record:"+resource+":"+recordID, tenantID, "record."+operation,
			map[string]string{"resource": resource, "record_id": recordID})
	}
}

func (h *Hub) feedChanged(ctx context.Context, key feedKey, module string) {
	h.changeMu.Lock()
	if h.changesClosed {
		h.changeMu.Unlock()
		return
	}
	if window := h.feedWindows[key]; window != nil {
		window.dirty = true
		h.changeMu.Unlock()
		return
	}
	if h.feedWindows == nil {
		h.feedWindows = make(map[feedKey]*feedWindow)
	}
	window := &feedWindow{}
	h.feedWindows[key] = window
	h.changeWG.Add(1)
	window.timer = time.AfterFunc(time.Second, func() {
		defer h.changeWG.Done()
		h.changeMu.Lock()
		delete(h.feedWindows, key)
		dirty := window.dirty && !h.changesClosed
		h.changeMu.Unlock()
		if dirty {
			h.publishChange(context.Background(), "feed:"+module, key.tenantID, "records.changed",
				map[string]string{"resource": key.resource})
		}
	})
	h.changeMu.Unlock()

	h.publishChange(ctx, "feed:"+module, key.tenantID, "records.changed", map[string]string{"resource": key.resource})
}

func (h *Hub) publishChange(ctx context.Context, channel, tenantID, msgType string, payload map[string]string) {
	ctx, cancel := context.WithTimeout(ctx, broadcastWriteTimeout)
	defer cancel()

	if _, err := h.BroadcastTenant(ctx, channel, tenantID, msgType, payload); err != nil {
		log.Warn().Err(err).Str("channel", channel).Msg("publish committed ORM change failed")
	}
}

func (h *Hub) stopChanges() {
	h.changeMu.Lock()
	defer h.changeMu.Unlock()

	h.changesClosed = true
	for _, window := range h.feedWindows {
		if window.timer.Stop() {
			h.changeWG.Done()
		}
	}
	clear(h.feedWindows)
}
