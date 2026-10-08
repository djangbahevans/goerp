// Package authmeupdate implements PATCH /auth/me for profile and appearance preferences.
// Only supplied fields change; avatar_id references a file uploaded through POST
// /storage/upload.
package authmeupdate

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/l10n/tenantl10n"
	"github.com/djangbahevans/goerp/internal/engine/role"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	tenants *tenantresolve.Resolver
	auth    *authcheck.Checker
	users   *user.Store
	members *role.Store
	files   *files.Store
	// locales supplies the tenant's available locales, the only ones a
	// user may pick as their locale.
	locales *tenantl10n.Store
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, users *user.Store, members *role.Store, filesStore *files.Store, locales *tenantl10n.Store) *Handler {
	return &Handler{tenants: tenants, auth: auth, users: users, members: members, files: filesStore, locales: locales}
}

// Longest phone and title parseMemberUpdate accepts.
const (
	maxPhoneLen = 64
	maxTitleLen = 200
)

// parseMemberUpdate reads phone and title, the per-tenant fields saved on
// the caller's member row in this tenant (shell-ux.md §4.1 "Shared and
// per-organisation fields"). null or a blank string clears one.
func parseMemberUpdate(body map[string]jsontext.Value) (role.MemberProfileUpdate, error) {
	var update role.MemberProfileUpdate
	var err error
	if update.SetPhone, update.Phone, err = optionalText(body, "phone", maxPhoneLen); err != nil {
		return update, err
	}
	if update.SetJobTitle, update.JobTitle, err = optionalText(body, "title", maxTitleLen); err != nil {
		return update, err
	}
	return update, nil
}

func optionalText(body map[string]jsontext.Value, field string, maxLen int) (bool, *string, error) {
	raw, ok := body[field]
	if !ok {
		return false, nil, nil
	}
	if raw.Kind() == 'n' {
		return true, nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, nil, &requestError{message: fmt.Sprintf("%q must be a string or null", field)}
	}
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > maxLen {
		return false, nil, &requestError{message: fmt.Sprintf("%q must be at most %d characters", field, maxLen)}
	}
	if value == "" {
		return true, nil, nil
	}
	return true, &value, nil
}

func writeUnauthenticated(w http.ResponseWriter, r *http.Request) {
	httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
}

func writeInvalidPreference(w http.ResponseWriter, r *http.Request, field, message string) {
	httperr.WriteDetails(r.Context(), w, http.StatusUnprocessableEntity, "invalid_preference", message, map[string]string{"field": field})
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
func parseUpdate(body map[string]jsontext.Value, availableLocales []string) (user.ProfileUpdate, error) {
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
	if raw, ok := body["contrast"]; ok {
		var contrast string
		if err := json.Unmarshal(raw, &contrast); err != nil || !slices.Contains(user.Contrasts, contrast) {
			return update, &requestError{field: "contrast", message: fmt.Sprintf("contrast must be one of %s", strings.Join(user.Contrasts, ", "))}
		}
		update.Contrast = &contrast
	}

	var err error
	if update.Locale, err = nullablePreference(body, "locale", func(locale string) string {
		if !slices.Contains(availableLocales, locale) {
			return fmt.Sprintf("locale must be one of %s", strings.Join(availableLocales, ", "))
		}
		return ""
	}); err != nil {
		return update, err
	}
	if update.Timezone, err = nullablePreference(body, "timezone", func(tz string) string {
		if !l10n.ValidTimezone(tz) {
			return "timezone must be an IANA time zone name"
		}
		return ""
	}); err != nil {
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

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantCtx, err := h.tenants.ResolveByHost(ctx, r.Host)
	if err != nil {
		switch {
		case errors.Is(err, tenantresolve.ErrTenantNotFound):
			httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "not found")
		case errors.Is(err, tenantresolve.ErrTenantSuspended):
			httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
		case errors.Is(err, tenantresolve.ErrTenantOffboarding):
			httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
		default:
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "update failed")
		}
		return
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeUnauthenticated(w, r)
		return
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if authcheck.WritePasswordChangeRequired(r.Context(), w, err) {
		return
	}
	if err != nil || !authCtx.IsAuthenticated {
		writeUnauthenticated(w, r)
		return
	}

	var body map[string]jsontext.Value
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	l10nSettings, err := h.locales.Load(ctx, tenantCtx.TenantID)
	if err != nil {
		log.Error().Err(err).Str("tenant_id", tenantCtx.TenantID).Msg("authmeupdate: load tenant locale settings")
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}
	update, err := parseUpdate(body, l10nSettings.AvailableLocales)
	var memberUpdate role.MemberProfileUpdate
	if err == nil {
		memberUpdate, err = parseMemberUpdate(body)
	}
	if err != nil {
		if reqErr, ok := errors.AsType[*requestError](err); ok && reqErr.field != "" {
			writeInvalidPreference(w, r, reqErr.field, reqErr.message)
			return
		}
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", err.Error())
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
			httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "storage_unavailable", "avatar updates are unavailable right now")
			return
		}
		f, err := h.files.GetByID(ctx, tenantCtx.Slug, *update.AvatarFileID)
		if err != nil {
			if errors.Is(err, files.ErrFileNotFound) {
				httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "avatar_id does not reference an uploaded file")
				return
			}
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "update failed")
			return
		}
		if f.DeletedAt != nil {
			httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "avatar_id does not reference an uploaded file")
			return
		}
	}

	oldAvatarID, err := h.users.UpdateProfile(ctx, authCtx.UserID, update)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}
	if err := h.members.UpdateMemberProfile(ctx, tenantCtx.Slug, authCtx.UserID, memberUpdate); err != nil {
		log.Error().Err(err).Str("user_id", authCtx.UserID).Msg("authmeupdate: member profile update failed")
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "update failed")
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
