package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

// /_notif/* (notification-system.md §9): the caller's own in-app feed.
// Engine-native, with no WASM dispatch and no module declaration. Every
// route is scoped to the caller: another user's notification id is a 404,
// the same as an id that doesn't exist.

const (
	notifFeedDefaultLimit = 50
	notifFeedMaxLimit     = 100
)

type notifResponse struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Module    string     `json:"module"`
	Title     string     `json:"title"`
	Body      *string    `json:"body"`
	ActionURL *string    `json:"action_url"`
	Icon      *string    `json:"icon"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type notifFeedMeta struct {
	Cursor  *string `json:"cursor"`
	HasMore bool    `json:"has_more"`
	Unread  int     `json:"unread"`
}

// notifCaller returns the request's auth and tenant context, writing a 503
// and returning false when either is unresolved.
func notifCaller(w http.ResponseWriter, r *http.Request) (*authcheck.AuthContext, *tenantresolve.TenantContext, bool) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return nil, nil, false
	}
	return authCtx, tenantCtx, true
}

// dispatchNotifFeedRoute is GET /_notif/feed's handler —
// ?cursor=&limit=&unread= pages the caller's non-dismissed notifications,
// unread first, then newest first.
func (e *Engine) dispatchNotifFeedRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := notifCaller(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	var cursor *notifications.Cursor
	if raw := q.Get("cursor"); raw != "" {
		c, err := notifications.ParseCursor(raw)
		if err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "malformed cursor")
			return
		}
		cursor = &c
	}
	limit := notifFeedDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > notifFeedMaxLimit {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer from 1 to 100")
			return
		}
		limit = n
	}
	unreadOnly := false
	if raw := q.Get("unread"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "unread must be true or false")
			return
		}
		unreadOnly = b
	}

	ctx := r.Context()
	items, hasMore, err := e.notificationStore.List(ctx, tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID, cursor, unreadOnly, limit)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "list notifications failed")
		return
	}
	unread, err := e.notificationStore.CountUnread(ctx, tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "list notifications failed")
		return
	}

	meta := notifFeedMeta{HasMore: hasMore, Unread: unread}
	if hasMore {
		meta.Cursor = new(notifications.CursorAfter(&items[len(items)-1]).String())
	}
	out := make([]notifResponse, 0, len(items))
	for i := range items {
		n := &items[i]
		out = append(out, notifResponse{
			ID:        n.ID,
			Type:      n.Type,
			Module:    n.Module,
			Title:     n.Title,
			Body:      n.Body,
			ActionURL: n.ActionURL,
			Icon:      n.Icon,
			ReadAt:    n.ReadAt,
			CreatedAt: n.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out, "meta": meta})
}

// dispatchNotifCountRoute is GET /_notif/count's handler — the caller's
// unread, non-dismissed total, for the bell badge.
func (e *Engine) dispatchNotifCountRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := notifCaller(w, r)
	if !ok {
		return
	}
	n, err := e.notificationStore.CountUnread(r.Context(), tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "count notifications failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

// dispatchNotifReadRoute is POST /_notif/{id}/read's handler.
func (e *Engine) dispatchNotifReadRoute(w http.ResponseWriter, r *http.Request) {
	e.notifUpdateOne(w, r, e.notificationStore.MarkRead, "mark notification read failed")
}

// dispatchNotifDismissRoute is DELETE /_notif/{id}'s handler.
func (e *Engine) dispatchNotifDismissRoute(w http.ResponseWriter, r *http.Request) {
	e.notifUpdateOne(w, r, e.notificationStore.Dismiss, "dismiss notification failed")
}

// dispatchNotifReadAllRoute is POST /_notif/read-all's handler.
func (e *Engine) dispatchNotifReadAllRoute(w http.ResponseWriter, r *http.Request) {
	e.notifUpdateAll(w, r, e.notificationStore.MarkAllRead, "mark all notifications read failed")
}

// dispatchNotifDismissAllRoute is DELETE /_notif/all's handler.
func (e *Engine) dispatchNotifDismissAllRoute(w http.ResponseWriter, r *http.Request) {
	e.notifUpdateAll(w, r, e.notificationStore.DismissAll, "dismiss all notifications failed")
}

// notifUpdateOne applies update to the caller's notification named by the
// {id} path parameter. Repeating it is a 204, since update keeps an
// existing timestamp.
func (e *Engine) notifUpdateOne(w http.ResponseWriter, r *http.Request, update func(ctx context.Context, tenantSlug, tenantID, userID, id string) error, failMsg string) {
	authCtx, tenantCtx, ok := notifCaller(w, r)
	if !ok {
		return
	}
	id := route.ParamsFromContext(r.Context())["id"]
	if id == "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		writeRouteError(w, http.StatusNotFound, "not_found", "notification not found")
		return
	}
	if err := update(r.Context(), tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID, id); err != nil {
		if errors.Is(err, notifications.ErrNotFound) {
			writeRouteError(w, http.StatusNotFound, "not_found", "notification not found")
			return
		}
		writeRouteError(w, http.StatusInternalServerError, "internal_error", failMsg)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (e *Engine) notifUpdateAll(w http.ResponseWriter, r *http.Request, update func(ctx context.Context, tenantSlug, tenantID, userID string) error, failMsg string) {
	authCtx, tenantCtx, ok := notifCaller(w, r)
	if !ok {
		return
	}
	if err := update(r.Context(), tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID); err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", failMsg)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notifDeviceTokenMaxLength bounds a registered token; FCM and APNs tokens
// are a few hundred bytes at most.
const notifDeviceTokenMaxLength = 4096

type notifDeviceTokenRequest struct {
	Platform   string `json:"platform"`
	Token      string `json:"token"`
	AppVersion string `json:"app_version"`
}

// dispatchNotifDeviceTokenRoute is POST /_notif/device-token's handler
// (notification-system.md §12) — registers the caller's push device
// token, or refreshes last_seen_at and app_version on a repeat.
func (e *Engine) dispatchNotifDeviceTokenRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := notifCaller(w, r)
	if !ok {
		return
	}

	var body notifDeviceTokenRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return
	}
	if !slices.Contains(notifications.DeviceTokenPlatforms, body.Platform) {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "platform must be ios, android or web")
		return
	}
	if body.Token == "" || len(body.Token) > notifDeviceTokenMaxLength {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "token must be 1 to 4096 bytes")
		return
	}

	if err := e.notificationStore.RegisterDeviceToken(r.Context(), tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID, body.Platform, body.Token, body.AppVersion); err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "register device token failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notifTypeMaxLength bounds a notification type name in a preferences
// PATCH; real ones are "<module>.<name>".
const notifTypeMaxLength = 200

type notifPreferencesResponse struct {
	AvailableChannels []string                          `json:"available_channels"`
	Global            notifications.Channels            `json:"global"`
	Types             map[string]notifications.Channels `json:"types"`
}

type notifPreferencesPatch struct {
	Global *notifications.ChannelsPatch           `json:"global"`
	Types  map[string]notifications.ChannelsPatch `json:"types"`
}

// dispatchNotifPreferencesRoute is GET /_notif/preferences's handler
// (notification-system.md §8): the tenant's available channels, the
// caller's global preferences, and the per-type ones that differ from
// global.
func (e *Engine) dispatchNotifPreferencesRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := notifCaller(w, r)
	if !ok {
		return
	}
	resp, err := e.notifPreferences(r.Context(), tenantCtx, authCtx.UserID)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "load notification preferences failed")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// dispatchNotifPreferencesUpdateRoute is PATCH /_notif/preferences's
// handler (shell-ux.md "API calls"): { global?, types? } upserts the
// caller's rows. Channels the tenant doesn't have available are accepted
// and ignored. Responds with the updated preferences.
func (e *Engine) dispatchNotifPreferencesUpdateRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := notifCaller(w, r)
	if !ok {
		return
	}

	var body notifPreferencesPatch
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object of { global?, types? }")
		return
	}
	for typ := range body.Types {
		if typ == "" || len(typ) > notifTypeMaxLength {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "notification type names must be 1 to 200 bytes")
			return
		}
	}

	ctx := r.Context()
	available, err := e.notificationStore.AvailableChannels(ctx, tenantCtx.TenantID)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "update notification preferences failed")
		return
	}
	keepAvailable := func(p notifications.ChannelsPatch) notifications.ChannelsPatch {
		if !slices.Contains(available, notifications.ChannelSMS) {
			p.SMS = nil
		}
		if !slices.Contains(available, notifications.ChannelPush) {
			p.Push = nil
		}
		return p
	}
	if body.Global != nil {
		body.Global = new(keepAvailable(*body.Global))
	}
	for typ, p := range body.Types {
		body.Types[typ] = keepAvailable(p)
	}

	if err := e.notificationStore.UpdatePreferences(ctx, tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID, body.Global, body.Types); err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "update notification preferences failed")
		return
	}
	resp, err := e.notifPreferences(ctx, tenantCtx, authCtx.UserID)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "load notification preferences failed")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (e *Engine) notifPreferences(ctx context.Context, tenantCtx *tenantresolve.TenantContext, userID string) (*notifPreferencesResponse, error) {
	available, err := e.notificationStore.AvailableChannels(ctx, tenantCtx.TenantID)
	if err != nil {
		return nil, err
	}
	prefs, err := e.notificationStore.Preferences(ctx, tenantCtx.Slug, tenantCtx.TenantID, userID)
	if err != nil {
		return nil, err
	}
	types := map[string]notifications.Channels{}
	for typ, c := range prefs.Types {
		if c != prefs.Global {
			types[typ] = c
		}
	}
	return &notifPreferencesResponse{AvailableChannels: available, Global: prefs.Global, Types: types}, nil
}
