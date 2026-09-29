package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/ws"
	"github.com/rs/zerolog/log"
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
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
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
			httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed cursor")
			return
		}
		cursor = &c
	}
	limit := notifFeedDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > notifFeedMaxLimit {
			httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "limit must be an integer from 1 to 100")
			return
		}
		limit = n
	}
	unreadOnly := false
	if raw := q.Get("unread"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "unread must be true or false")
			return
		}
		unreadOnly = b
	}

	ctx := r.Context()
	items, hasMore, err := e.notificationStore.List(ctx, tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID, cursor, unreadOnly, limit)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "list notifications failed")
		return
	}
	unread, err := e.notificationStore.CountUnread(ctx, tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "list notifications failed")
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
	writeJSON(ctx, w, http.StatusOK, map[string]any{"data": out, "meta": meta})
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
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "count notifications failed")
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]int{"count": n})
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
// {id} path parameter, then tells the caller's other sessions in the
// tenant with notification.read (notification-system.md §9, which uses it
// for a dismissal too). Repeating it is a 204, since update keeps an
// existing timestamp.
func (e *Engine) notifUpdateOne(w http.ResponseWriter, r *http.Request, update func(ctx context.Context, tenantSlug, tenantID, userID, id string) error, failMsg string) {
	authCtx, tenantCtx, ok := notifCaller(w, r)
	if !ok {
		return
	}
	id := route.ParamsFromContext(r.Context())["id"]
	if id == "" {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "notification not found")
		return
	}
	if err := update(r.Context(), tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID, id); err != nil {
		if errors.Is(err, notifications.ErrNotFound) {
			httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "notification not found")
			return
		}
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", failMsg)
		return
	}
	e.notifBroadcast(r.Context(), tenantCtx.TenantID, authCtx.UserID, "notification.read", map[string]string{"id": id})
	w.WriteHeader(http.StatusNoContent)
}

// notifUpdateAll applies update to every notification of the caller's,
// then tells their other sessions with notification.read_all.
func (e *Engine) notifUpdateAll(w http.ResponseWriter, r *http.Request, update func(ctx context.Context, tenantSlug, tenantID, userID string) error, failMsg string) {
	authCtx, tenantCtx, ok := notifCaller(w, r)
	if !ok {
		return
	}
	if err := update(r.Context(), tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID); err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", failMsg)
		return
	}
	e.notifBroadcast(r.Context(), tenantCtx.TenantID, authCtx.UserID, "notification.read_all", nil)
	w.WriteHeader(http.StatusNoContent)
}

// notifBroadcast sends msgType on userID's user channel to their sessions
// in tenantID. The change is already stored, so a push that reaches no
// session is not an error.
func (e *Engine) notifBroadcast(ctx context.Context, tenantID, userID, msgType string, payload any) {
	if e.wsHub == nil {
		return
	}
	_, _ = e.wsHub.BroadcastUser(ctx, ws.NotificationsChannel, tenantID, userID, msgType, payload)
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
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return
	}
	if !slices.Contains(notifications.DeviceTokenPlatforms, body.Platform) {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "platform must be ios, android or web")
		return
	}
	if body.Token == "" || len(body.Token) > notifDeviceTokenMaxLength {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "token must be 1 to 4096 bytes")
		return
	}

	if err := e.notificationStore.RegisterDeviceToken(r.Context(), tenantCtx.Slug, tenantCtx.TenantID, authCtx.UserID, body.Platform, body.Token, body.AppVersion); err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "register device token failed")
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
// caller's global preferences, and every type they have their own
// preferences for.
func (e *Engine) dispatchNotifPreferencesRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := notifCaller(w, r)
	if !ok {
		return
	}
	resp, err := e.notifPreferences(r.Context(), tenantCtx, authCtx.UserID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "load notification preferences failed")
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, resp)
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
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object of { global?, types? }")
		return
	}
	for typ := range body.Types {
		if typ == "" || len(typ) > notifTypeMaxLength {
			httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "notification type names must be 1 to 200 bytes")
			return
		}
	}

	ctx := r.Context()
	available, err := e.notificationStore.AvailableChannels(ctx, tenantCtx.TenantID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "update notification preferences failed")
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
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "update notification preferences failed")
		return
	}
	resp, err := e.notifPreferences(ctx, tenantCtx, authCtx.UserID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "load notification preferences failed")
		return
	}
	writeJSON(ctx, w, http.StatusOK, resp)
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
	return &notifPreferencesResponse{AvailableChannels: available, Global: prefs.Global, Types: prefs.Types}, nil
}

// notifUnsubscribePage is the whole page /_notif/unsubscribe answers with.
// With a Token it is the confirmation step: a button that POSTs the token
// back. Every field is escaped.
var notifUnsubscribePage = template.Must(template.New("unsubscribe").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Heading}}</title>
<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;color:#1f2328}h1{font-size:1.25rem}button{font:inherit;padding:.5rem 1rem;cursor:pointer}</style>
</head>
<body>
<h1>{{.Heading}}</h1>
<p>{{.Message}}</p>
{{if .Token}}<form method="post" action="/_notif/unsubscribe">
<input type="hidden" name="token" value="{{.Token}}">
<button type="submit">Unsubscribe</button>
</form>{{end}}
</body>
</html>
`))

type notifUnsubscribePageData struct {
	Heading, Message, Token string
}

func writeNotifUnsubscribePage(w http.ResponseWriter, status int, data notifUnsubscribePageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// The token is in this page's URL; keep it out of any Referer.
	w.Header().Set("Referrer-Policy", "no-referrer")
	// No scripts, and no framing: the confirm button can't be clickjacked.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_ = notifUnsubscribePage.Execute(w, data)
}

// notifUnsubscribeTarget resolves the tenant from Host and verifies token
// against it, writing the error page and returning false when either
// fails. A token for another tenant is as invalid as a tampered one.
func (e *Engine) notifUnsubscribeTarget(w http.ResponseWriter, r *http.Request, token string) (*tenantresolve.TenantContext, *notifications.UnsubscribeClaims, bool) {
	tenantCtx, err := e.tenantResolver.ResolveByHost(r.Context(), r.Host)
	if err != nil {
		writeNotifUnsubscribePage(w, http.StatusNotFound, notifUnsubscribePageData{
			Heading: "Workspace not found",
			Message: "This unsubscribe link doesn't belong to a workspace at this address.",
		})
		return nil, nil, false
	}
	claims, err := e.unsubscribeCodec.Verify(token)
	if err != nil || claims.TenantID != tenantCtx.TenantID {
		writeNotifUnsubscribePage(w, http.StatusBadRequest, notifUnsubscribePageData{
			Heading: "Invalid unsubscribe link",
			Message: "This unsubscribe link is invalid or has expired. You can change which emails you get in your notification settings.",
		})
		return nil, nil, false
	}
	return tenantCtx, claims, true
}

// dispatchNotifUnsubscribeRoute is GET /_notif/unsubscribe's handler: the
// link in notification emails (notification-system.md §10). It only asks
// for confirmation — mail scanners and link prefetchers open every link
// in an email, so a GET must not unsubscribe anyone. EngineBuiltin, with
// no session: the tenant comes from Host, the user and notification type
// from the signed token.
func (e *Engine) dispatchNotifUnsubscribeRoute(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if _, _, ok := e.notifUnsubscribeTarget(w, r, token); !ok {
		return
	}
	writeNotifUnsubscribePage(w, http.StatusOK, notifUnsubscribePageData{
		Heading: "Unsubscribe from these emails?",
		Message: "You'll still see these notifications in the app.",
		Token:   token,
	})
}

// notifUnsubscribeMaxBody bounds POST /_notif/unsubscribe's form body.
const notifUnsubscribeMaxBody = 64 << 10

// dispatchNotifUnsubscribeConfirmRoute is POST /_notif/unsubscribe's
// handler: the confirmation page's button, and RFC 8058 one-click
// unsubscribe (a mail client POSTing "List-Unsubscribe=One-Click" to the
// link, token still in its query). Turns off email for the token's
// notification type; repeating it succeeds.
func (e *Engine) dispatchNotifUnsubscribeConfirmRoute(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, notifUnsubscribeMaxBody)
	tenantCtx, claims, ok := e.notifUnsubscribeTarget(w, r, r.FormValue("token"))
	if !ok {
		return
	}

	if err := e.notificationStore.Unsubscribe(r.Context(), tenantCtx.Slug, tenantCtx.TenantID, claims.Subject, claims.NotificationType); err != nil {
		log.Error().Err(err).Str("tenant", tenantCtx.Slug).Str("user_id", claims.Subject).Msg("notification unsubscribe failed")
		writeNotifUnsubscribePage(w, http.StatusInternalServerError, notifUnsubscribePageData{
			Heading: "Something went wrong",
			Message: "We couldn't unsubscribe you just now. Please try the link again later.",
		})
		return
	}
	writeNotifUnsubscribePage(w, http.StatusOK, notifUnsubscribePageData{
		Heading: "You're unsubscribed",
		Message: "You won't get these emails any more. You'll still see these notifications in the app, and you can turn the emails back on in your notification settings.",
	})
}
