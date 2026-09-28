package engine

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/djangbahevans/goerp/internal/engine/activitytype"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

// /_meta/activity-types and /admin/activity-types (scheduled-activities.md
// §9): the tenant's scheduled-activity types, resolved for the caller's
// locale for every member, and with their untranslated maps and usage for
// the tenant's admins to add, edit, archive, reorder and delete.

const (
	activityTypeMaxLabel   = 60
	activityTypeMaxDueDays = 365
	adminRoleName          = "admin"
)

type activityTypeResponse struct {
	Key            string  `json:"key"`
	Label          string  `json:"label"`
	Icon           string  `json:"icon"`
	DefaultSummary *string `json:"default_summary"`
	DefaultDueDays *int    `json:"default_due_days"`
	Archived       bool    `json:"archived"`
}

type adminActivityTypeResponse struct {
	Key            string            `json:"key"`
	Label          map[string]string `json:"label"`
	Icon           string            `json:"icon"`
	DefaultSummary map[string]string `json:"default_summary"`
	DefaultDueDays *int              `json:"default_due_days"`
	Archived       bool              `json:"archived"`
	UsageCount     int               `json:"usage_count"`
}

type adminActivityTypeCreateRequest struct {
	Key            string            `json:"key"`
	Label          map[string]string `json:"label"`
	Icon           string            `json:"icon"`
	DefaultSummary map[string]string `json:"default_summary"`
	DefaultDueDays *int              `json:"default_due_days"`
}

// adminActivityTypePatchRequest's raw fields tell an explicit null (clear
// it) apart from an absent key (leave it).
type adminActivityTypePatchRequest struct {
	Label          jsontext.Value `json:"label,omitzero"`
	Icon           *string        `json:"icon"`
	DefaultSummary jsontext.Value `json:"default_summary,omitzero"`
	DefaultDueDays jsontext.Value `json:"default_due_days,omitzero"`
	Archived       *bool          `json:"archived"`
}

type adminActivityTypeOrderRequest struct {
	Keys []string `json:"keys"`
}

// dispatchActivityTypeListRoute is GET /_meta/activity-types' handler —
// every type, archived included, in position order, with its label and
// default summary resolved for the caller's locale.
func (e *Engine) dispatchActivityTypeListRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := requestContexts(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	types, err := e.activityTypeStore.List(ctx, tenantCtx.Slug, false)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "list activity types failed")
		return
	}
	locale, tenantDefault := e.requestLocale(ctx, authCtx, tenantCtx), tenantDefaultLocale(tenantCtx)
	out := make([]activityTypeResponse, len(types))
	for i, t := range types {
		label, _ := activitytype.Resolve(t.Label, locale, tenantDefault)
		var summary *string
		if s, ok := activitytype.Resolve(t.DefaultSummary, locale, tenantDefault); ok {
			summary = &s
		}
		out[i] = activityTypeResponse{Key: t.Key, Label: label, Icon: t.Icon, DefaultSummary: summary, DefaultDueDays: t.DefaultDueDays, Archived: t.Archived()}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// dispatchAdminActivityTypeListRoute is GET /admin/activity-types' handler.
func (e *Engine) dispatchAdminActivityTypeListRoute(w http.ResponseWriter, r *http.Request) {
	_, tenantCtx, ok := adminRequestContexts(w, r)
	if !ok {
		return
	}
	e.writeAdminActivityTypeList(w, r.Context(), tenantCtx)
}

// dispatchAdminActivityTypeCreateRoute is POST /admin/activity-types'
// handler — adds a type after every existing one.
func (e *Engine) dispatchAdminActivityTypeCreateRoute(w http.ResponseWriter, r *http.Request) {
	_, tenantCtx, ok := adminRequestContexts(w, r)
	if !ok {
		return
	}
	var body adminActivityTypeCreateRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return
	}
	if !activitytype.KeyPattern.MatchString(body.Key) {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "key must be 1 to 40 lowercase letters, digits or underscores, starting with a letter")
		return
	}
	// PUT /admin/activity-types/order owns that path, so a type keyed
	// "order" could never be patched or deleted.
	if body.Key == "order" {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", `key "order" is reserved`)
		return
	}
	label, msg := validActivityTypeLabel(body.Label, tenantDefaultLocale(tenantCtx))
	if msg != "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	summary, msg := validLocaleTextMap("default_summary", body.DefaultSummary, scheduledActivityMaxSummary)
	if msg != "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	if msg := validDefaultDueDays(body.DefaultDueDays); msg != "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	if !activitytype.ValidIcon(body.Icon) {
		writeRouteError(w, http.StatusBadRequest, "invalid_icon", "icon must be a Lucide icon name")
		return
	}

	t, err := e.activityTypeStore.Create(r.Context(), tenantCtx.Slug, activitytype.NewType{
		Key:            body.Key,
		Label:          label,
		Icon:           body.Icon,
		DefaultSummary: summary,
		DefaultDueDays: body.DefaultDueDays,
	})
	if err != nil {
		writeActivityTypeStoreError(w, err, "create activity type failed")
		return
	}
	writeJSON(w, http.StatusCreated, adminActivityTypeToResponse(t))
}

// dispatchAdminActivityTypeUpdateRoute is PATCH /admin/activity-types/{key}'s
// handler — relabels, re-icons, changes the defaults of, archives or
// restores a type. Its key never changes.
func (e *Engine) dispatchAdminActivityTypeUpdateRoute(w http.ResponseWriter, r *http.Request) {
	_, tenantCtx, ok := adminRequestContexts(w, r)
	if !ok {
		return
	}
	key, ok := activityTypeKeyParam(w, r)
	if !ok {
		return
	}
	var body adminActivityTypePatchRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return
	}

	u := activitytype.Update{Icon: body.Icon, Archived: body.Archived}
	if body.Label != nil {
		var raw map[string]string
		if err := json.Unmarshal(body.Label, &raw); err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "label must be an object of locale to text")
			return
		}
		label, msg := validActivityTypeLabel(raw, tenantDefaultLocale(tenantCtx))
		if msg != "" {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
		u.Label = label
	}
	if body.DefaultSummary != nil {
		var raw map[string]string
		if err := json.Unmarshal(body.DefaultSummary, &raw); err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "default_summary must be an object of locale to text, or null")
			return
		}
		summary, msg := validLocaleTextMap("default_summary", raw, scheduledActivityMaxSummary)
		if msg != "" {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
		u.DefaultSummary = summary
	}
	if body.DefaultDueDays != nil {
		var days *int
		if err := json.Unmarshal(body.DefaultDueDays, &days); err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "default_due_days must be an integer or null")
			return
		}
		if msg := validDefaultDueDays(days); msg != "" {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
		u.DefaultDueDays, u.ClearDefaultDueDays = days, days == nil
	}
	if body.Icon != nil && !activitytype.ValidIcon(*body.Icon) {
		writeRouteError(w, http.StatusBadRequest, "invalid_icon", "icon must be a Lucide icon name")
		return
	}

	t, err := e.activityTypeStore.Update(r.Context(), tenantCtx.Slug, key, u)
	if err != nil {
		writeActivityTypeStoreError(w, err, "update activity type failed")
		return
	}
	writeJSON(w, http.StatusOK, adminActivityTypeToResponse(t))
}

// dispatchAdminActivityTypeReorderRoute is PUT /admin/activity-types/order's
// handler — keys lists every type in its new order. Responds with the
// reordered list, as GET does.
func (e *Engine) dispatchAdminActivityTypeReorderRoute(w http.ResponseWriter, r *http.Request) {
	_, tenantCtx, ok := adminRequestContexts(w, r)
	if !ok {
		return
	}
	var body adminActivityTypeOrderRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil || body.Keys == nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", `request body must be {"keys": [...]}`)
		return
	}
	ctx := r.Context()
	if err := e.activityTypeStore.Reorder(ctx, tenantCtx.Slug, body.Keys); err != nil {
		writeActivityTypeStoreError(w, err, "reorder activity types failed")
		return
	}
	e.writeAdminActivityTypeList(w, ctx, tenantCtx)
}

// dispatchAdminActivityTypeDeleteRoute is DELETE
// /admin/activity-types/{key}'s handler — only for a type no activity
// uses; one in use is archived instead.
func (e *Engine) dispatchAdminActivityTypeDeleteRoute(w http.ResponseWriter, r *http.Request) {
	_, tenantCtx, ok := adminRequestContexts(w, r)
	if !ok {
		return
	}
	key, ok := activityTypeKeyParam(w, r)
	if !ok {
		return
	}
	if err := e.activityTypeStore.Delete(r.Context(), tenantCtx.Slug, key); err != nil {
		writeActivityTypeStoreError(w, err, "delete activity type failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (e *Engine) writeAdminActivityTypeList(w http.ResponseWriter, ctx context.Context, tenantCtx *tenantresolve.TenantContext) {
	types, err := e.activityTypeStore.List(ctx, tenantCtx.Slug, true)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "list activity types failed")
		return
	}
	out := make([]adminActivityTypeResponse, len(types))
	for i := range types {
		out[i] = adminActivityTypeToResponse(&types[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// adminRequestContexts is requestContexts for a route that needs the admin
// role in the tenant, the same check internal/engine/auth/adminroles makes.
func adminRequestContexts(w http.ResponseWriter, r *http.Request) (*authcheck.AuthContext, *tenantresolve.TenantContext, bool) {
	authCtx, tenantCtx, ok := requestContexts(w, r)
	if !ok {
		return nil, nil, false
	}
	if !slices.Contains(authCtx.RolesLive, adminRoleName) {
		writeRouteError(w, http.StatusForbidden, "forbidden", "admin role required")
		return nil, nil, false
	}
	return authCtx, tenantCtx, true
}

// activityTypeKeyParam returns the {key} path parameter, writing 404 for
// one no type could have.
func activityTypeKeyParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := route.ParamsFromContext(r.Context())["key"]
	if !activitytype.KeyPattern.MatchString(key) {
		writeRouteError(w, http.StatusNotFound, "not_found", "activity type not found")
		return "", false
	}
	return key, true
}

// requestLocale is the caller's locale (l10n-guide.md §2 "Per-request
// locale"): their profile's, then the tenant default.
func (e *Engine) requestLocale(ctx context.Context, authCtx *authcheck.AuthContext, tenantCtx *tenantresolve.TenantContext) string {
	if profile, err := e.userStore.GetProfile(ctx, authCtx.UserID); err == nil && profile.Locale != nil {
		return *profile.Locale
	}
	return tenantDefaultLocale(tenantCtx)
}

// tenantDefaultLocale is the tenant's default locale. No tenant setting
// for it exists yet, so every tenant has the platform default, as GET
// /auth/me reports (l10n-guide.md §2 "Tenant default locale").
func tenantDefaultLocale(*tenantresolve.TenantContext) string {
	return l10n.PlatformDefaultLocale
}

// validActivityTypeLabel checks a label map: 1–60 characters per locale
// and an entry for the tenant's default locale.
func validActivityTypeLabel(m map[string]string, tenantDefault string) (map[string]string, string) {
	label, msg := validLocaleTextMap("label", m, activityTypeMaxLabel)
	if msg != "" {
		return nil, msg
	}
	if _, ok := label[tenantDefault]; !ok {
		return nil, fmt.Sprintf("label must have an entry for the tenant's default locale %q", tenantDefault)
	}
	return label, ""
}

// validLocaleTextMap checks m's keys are locale tags and trims each value,
// which must be 1 to maxLen characters. A nil m is an empty map.
func validLocaleTextMap(field string, m map[string]string, maxLen int) (map[string]string, string) {
	out := make(map[string]string, len(m))
	for locale, text := range m {
		if !l10n.ValidLocale(locale) {
			return nil, fmt.Sprintf("%s has an unknown locale tag %q", field, locale)
		}
		text = strings.TrimSpace(text)
		if text == "" || utf8.RuneCountInString(text) > maxLen {
			return nil, fmt.Sprintf("%s must be 1 to %d characters in every locale", field, maxLen)
		}
		out[locale] = text
	}
	return out, ""
}

func validDefaultDueDays(days *int) string {
	if days != nil && (*days < 0 || *days > activityTypeMaxDueDays) {
		return "default_due_days must be from 0 to 365"
	}
	return ""
}

func adminActivityTypeToResponse(t *activitytype.Type) adminActivityTypeResponse {
	return adminActivityTypeResponse{
		Key:            t.Key,
		Label:          t.Label,
		Icon:           t.Icon,
		DefaultSummary: t.DefaultSummary,
		DefaultDueDays: t.DefaultDueDays,
		Archived:       t.Archived(),
		UsageCount:     t.UsageCount,
	}
}

func writeActivityTypeStoreError(w http.ResponseWriter, err error, internalMsg string) {
	switch {
	case errors.Is(err, activitytype.ErrNotFound):
		writeRouteError(w, http.StatusNotFound, "not_found", "activity type not found")
	case errors.Is(err, activitytype.ErrKeyTaken):
		writeRouteError(w, http.StatusConflict, "type_key_taken", "an activity type with that key already exists")
	case errors.Is(err, activitytype.ErrLimitReached):
		writeRouteError(w, http.StatusConflict, "type_limit_reached", fmt.Sprintf("a tenant can have at most %d activity types", activitytype.MaxTypes))
	case errors.Is(err, activitytype.ErrLastActive):
		writeRouteError(w, http.StatusConflict, "last_active_type", "the tenant must keep at least one active activity type")
	case errors.Is(err, activitytype.ErrInUse):
		writeRouteError(w, http.StatusConflict, "type_in_use", "the activity type is in use; archive it instead")
	case errors.Is(err, activitytype.ErrOrderMismatch):
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "keys must list every activity type key exactly once")
	default:
		writeRouteError(w, http.StatusInternalServerError, "internal_error", internalMsg)
	}
}
