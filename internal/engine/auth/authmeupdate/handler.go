// Package authmeupdate implements PATCH /auth/me — the self-service save
// behind the /settings/profile page (shell-ux.md §4.1) and the Appearance
// page's preferences (§4.4). Every field is optional and only the fields
// present change. Same tenant/auth resolution pattern as authme.Handler.
//
// shell-ux.md §4.1 also documents a separate POST /auth/me/avatar
// (multipart/form-data) endpoint for the avatar upload itself, but
// FileField (goerp#714, shell/packages/sdk/src/components/file-field.tsx)
// — the only real caller of any avatar upload flow — always uploads to
// POST /storage/upload directly (file-field-upload.ts's uploadFile has no
// configurable endpoint) and hands the caller back a file_id. There is no
// second multipart endpoint here: this handler instead accepts that
// file_id as avatar_id in the same PATCH body as name, matching the
// avatar_id field-naming convention shell-ux.md's own earlier form-field
// example already uses.
package authmeupdate

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/files"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	tenants *tenantresolve.Resolver
	auth    *authcheck.Checker
	users   *user.Store
	files   *files.Store
	// availableLocales is what a user may pick as their locale: the
	// platform's GOERP_AVAILABLE_LOCALES until tenants have locale settings.
	availableLocales []string
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, users *user.Store, filesStore *files.Store, availableLocales []string) *Handler {
	return &Handler{tenants: tenants, auth: auth, users: users, files: filesStore, availableLocales: availableLocales}
}

func writeJSON(w http.ResponseWriter, v any) {
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeUnauthenticated(w http.ResponseWriter) {
	writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
}

func writeInvalidPreference(w http.ResponseWriter, field, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	writeJSON(w, map[string]any{
		"error": map[string]any{"code": "invalid_preference", "message": message, "details": map[string]string{"field": field}},
	})
}

// requestError is a body the handler rejects: status 400 invalid_request,
// or, with field set, 422 invalid_preference naming that field.
type requestError struct {
	field   string
	message string
}

func (e *requestError) Error() string { return e.message }

// parseUpdate reads the PATCH body into a user.ProfileUpdate. A key's
// absence and an explicit null are different: absent leaves the field
// untouched, null resets locale/timezone/date_format to inherit. avatar_id
// keeps its three states (user.ProfileUpdate): absent or null leaves the
// avatar, "" clears it, a file id sets it. FileField's "remove" action must
// be able to reach the "" case — without it, clearing an avatar in the UI
// and saving would be indistinguishable from never touching the avatar
// field at all.
func (h *Handler) parseUpdate(body map[string]jsontext.Value) (user.ProfileUpdate, error) {
	var update user.ProfileUpdate

	if raw, ok := body["name"]; ok {
		var name string
		if err := json.Unmarshal(raw, &name); err != nil || strings.TrimSpace(name) == "" {
			return update, &requestError{message: `"name" must be a non-empty string`}
		}
		update.Name = new(strings.TrimSpace(name))
	}
	if raw, ok := body["avatar_id"]; ok && raw.Kind() != 'n' {
		var avatarID string
		if err := json.Unmarshal(raw, &avatarID); err != nil {
			return update, &requestError{message: `"avatar_id" must be a string`}
		}
		update.AvatarFileID = &avatarID
	}
	if raw, ok := body["theme"]; ok {
		var theme string
		if err := json.Unmarshal(raw, &theme); err != nil || !slices.Contains(user.Themes, theme) {
			return update, &requestError{field: "theme", message: fmt.Sprintf("theme must be one of %s", strings.Join(user.Themes, ", "))}
		}
		update.Theme = &theme
	}

	var err error
	if update.Locale, err = nullablePreference(body, "locale", func(locale string) string {
		if !slices.Contains(h.availableLocales, locale) {
			return fmt.Sprintf("locale must be one of %s", strings.Join(h.availableLocales, ", "))
		}
		return ""
	}); err != nil {
		return update, err
	}
	if update.Timezone, err = nullablePreference(body, "timezone", validTimezone); err != nil {
		return update, err
	}
	if update.DateFormat, err = nullablePreference(body, "date_format", func(format string) string {
		if !slices.Contains(user.DateFormats, format) {
			return fmt.Sprintf("date_format must be one of %s", strings.Join(user.DateFormats, ", "))
		}
		return ""
	}); err != nil {
		return update, err
	}

	return update, nil
}

// nullablePreference reads a preference that null resets to inherit.
// check returns why a value is rejected, or "" to accept it.
func nullablePreference(body map[string]jsontext.Value, field string, check func(string) string) (user.NullableField, error) {
	raw, ok := body[field]
	if !ok {
		return user.NullableField{}, nil
	}
	if raw.Kind() == 'n' {
		return user.NullableField{Set: true}, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return user.NullableField{}, &requestError{field: field, message: field + " must be a string or null"}
	}
	if problem := check(value); problem != "" {
		return user.NullableField{}, &requestError{field: field, message: problem}
	}
	return user.NullableField{Set: true, Value: &value}, nil
}

// validTimezone accepts an IANA zone name. time.LoadLocation also accepts
// "" and "Local", which name no zone a browser can render.
func validTimezone(tz string) string {
	if tz == "" || tz == "Local" {
		return "timezone must be an IANA time zone name"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "timezone must be an IANA time zone name"
	}
	return ""
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantCtx, err := h.tenants.ResolveByHost(ctx, r.Host)
	if err != nil {
		switch {
		case errors.Is(err, tenantresolve.ErrTenantNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		case errors.Is(err, tenantresolve.ErrTenantSuspended):
			writeJSONError(w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
		case errors.Is(err, tenantresolve.ErrTenantOffboarding):
			writeJSONError(w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
		default:
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "update failed")
		}
		return
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeUnauthenticated(w)
		return
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		writeUnauthenticated(w)
		return
	}

	var body map[string]jsontext.Value
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	update, err := h.parseUpdate(body)
	if err != nil {
		if reqErr, ok := errors.AsType[*requestError](err); ok && reqErr.field != "" {
			writeInvalidPreference(w, reqErr.field, reqErr.message)
			return
		}
		writeJSONError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// A real avatar_id (not the "" clear sentinel) must reference a real,
	// non-deleted file already uploaded to this tenant (via POST
	// /storage/upload) — otherwise any authenticated user could set
	// avatar_id to an arbitrary UUID and have GET /auth/me attempt to
	// resolve and serve it.
	if update.AvatarFileID != nil && *update.AvatarFileID != "" {
		if h.files == nil {
			log.Warn().Str("user_id", authCtx.UserID).Msg("authmeupdate: no files.Store configured, rejecting avatar_id")
			writeJSONError(w, http.StatusServiceUnavailable, "storage_unavailable", "avatar updates are unavailable right now")
			return
		}
		f, err := h.files.GetByID(ctx, tenantCtx.Slug, *update.AvatarFileID)
		if err != nil {
			if errors.Is(err, files.ErrFileNotFound) {
				writeJSONError(w, http.StatusBadRequest, "invalid_request", "avatar_id does not reference an uploaded file")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "update failed")
			return
		}
		if f.DeletedAt != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "avatar_id does not reference an uploaded file")
			return
		}
	}

	oldAvatarID, err := h.users.UpdateProfile(ctx, authCtx.UserID, update)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}

	// Best-effort: the profile save already succeeded, so a cleanup
	// failure here only leaves the old file un-marked, not a request
	// failure — object-storage-guide.md §10's "Manual deletion" is
	// explicitly the same best-effort shape (the actual storage object is
	// removed by a separate async sweep, not synchronously here). Covers
	// both a real replacement and a clear: for a clear, AvatarFileID
	// points at "", which is never equal to a real stored file id, so
	// oldAvatarID still gets marked deleted correctly.
	if oldAvatarID != nil && update.AvatarFileID != nil && *oldAvatarID != *update.AvatarFileID {
		if err := h.files.MarkDeleted(ctx, tenantCtx.Slug, *oldAvatarID); err != nil {
			log.Warn().Err(err).Str("user_id", authCtx.UserID).Str("file_id", *oldAvatarID).Msg("authmeupdate: failed to mark replaced avatar deleted")
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
