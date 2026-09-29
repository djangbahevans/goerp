// Package authme implements GET /auth/me — the check the shell's client-
// side auth state machine (shell-architecture.md §7) runs on mount to
// find out whether an existing session is still valid, without a login
// form. Class A (auth-internals.md §9 "Route classes"): standard Host-
// header tenant resolution, standard JWT-or-API-key branch. The generic
// middleware pipeline that would normally run those two steps ahead of
// every Class A route doesn't exist yet (goerp#91, still blocked); this
// handler calls the same underlying primitives directly instead —
// tenantresolve.Resolver.ResolveByHost, then authcheck.Checker — the same
// "call the primitive directly, skip the not-yet-built generic
// middleware" pattern loginflow/mfareverify/mfaverify already use.
// goerp#91/#224 will later lift this same logic into the automatic
// per-request pipeline; nothing here needs to be unwound when that
// happens.
package authme

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/l10n/tenantl10n"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/rs/zerolog/log"
)

// avatarURLExpiry matches object-storage-guide.md §6's own "Generate a
// signed URL valid for 1 hour" example — long enough that a mount-time
// GET /auth/me's URL stays valid through a typical session, short enough
// that a since-replaced or deleted avatar stops being servable promptly.
const avatarURLExpiry = time.Hour

type Handler struct {
	tenants  *tenantresolve.Resolver
	auth     *authcheck.Checker
	users    *user.Store
	members  *role.Store
	files    *files.Store
	backend  storage.Backend
	locales  *tenantl10n.Store
	policies *password.PolicyStore
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, users *user.Store, members *role.Store, filesStore *files.Store, backend storage.Backend, locales *tenantl10n.Store, policies *password.PolicyStore) *Handler {
	return &Handler{tenants: tenants, auth: auth, users: users, members: members, files: filesStore, backend: backend, locales: locales, policies: policies}
}

// writeJSON matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own: '<', '>', '&' escaped for
// safe HTML embedding, and U+2028/U+2029 escaped for safe JS embedding.
func writeJSON(w http.ResponseWriter, v any) {
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeUnauthenticated(w http.ResponseWriter, r *http.Request) {
	httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
}

type meResponse struct {
	User   meUser   `json:"user"`
	Tenant meTenant `json:"tenant"`
}

// meUser is typescript-sdk-reference.md's CurrentUser on the wire.
// Name/AvatarURL come from user.Store.GetProfile (goerp#817) and are nil for
// a user with no system.user_profiles row, or whose row holds
// user.UpdateProfile's placeholder name — the frontend falls back to a
// derived display name in that case rather than this handler inventing
// one. AvatarURL is a freshly-generated signed URL (goerp#819), resolved
// from Profile.AvatarFileID on every request — never persisted, since
// signed URLs expire. A nil Locale/Timezone/DateFormat inherits the
// tenant default. ContactID, Phone and Title are this tenant's own values,
// from the caller's tenant_members row (auth-internals.md §2 "Tenant
// members").
type meUser struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	ContactID     *string    `json:"contact_id"`
	Phone         *string    `json:"phone"`
	Title         *string    `json:"title"`
	Name          *string    `json:"name"`
	AvatarURL     *string    `json:"avatar_url"`
	Roles         []string   `json:"roles"`
	AMR           []string   `json:"amr"`
	MFAVerifiedAt *time.Time `json:"mfa_verified_at"`
	// MFASetupRequired drives the shell's forced-enrollment redirect
	// (auth-internals.md §8 "MFA enrollment").
	MFASetupRequired bool `json:"mfa_setup_required"`
	// PasswordChangeRequired reports a session restricted until the
	// password is changed, which the shell routes to the change-password
	// page (auth-internals.md §3 "Password policy at sign-in").
	PasswordChangeRequired bool `json:"password_change_required"`
	// PasswordMinLength is the account's combined minimum, the one a new
	// password must meet (auth-internals.md §3 "Password strength
	// validation").
	PasswordMinLength int     `json:"password_min_length"`
	Theme             string  `json:"theme"`
	Contrast          string  `json:"contrast"`
	Locale            *string `json:"locale"`
	Timezone          *string `json:"timezone"`
	DateFormat        *string `json:"date_format"`
}

// meTenant is CurrentTenant on the wire, its locale fields the tenant's
// effective locale settings (l10n-guide.md §2 "Tenant default locale").
type meTenant struct {
	ID               string   `json:"id"`
	Slug             string   `json:"slug"`
	Name             string   `json:"name"`
	Plan             string   `json:"plan"`
	DefaultLocale    string   `json:"default_locale"`
	DefaultTimezone  string   `json:"default_timezone"`
	AvailableLocales []string `json:"available_locales"`
	// PasswordMinLength is the tenant's own minimum password length
	// (auth-internals.md §3 "Password strength validation").
	PasswordMinLength int `json:"password_min_length"`
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
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "session check failed")
		}
		return
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeUnauthenticated(w, r)
		return
	}
	authCtx, err := h.auth.AuthenticateAllowingPasswordChange(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		writeUnauthenticated(w, r)
		return
	}

	u, err := h.users.GetByID(ctx, authCtx.UserID)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			writeUnauthenticated(w, r)
			return
		}
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "session check failed")
		return
	}

	// A profile lookup failure degrades to nil name/avatarUrl and default
	// preferences (the frontend already derives a display name for that
	// case) rather than failing the whole session check — id/email/roles
	// above already resolved successfully, and these fields are cosmetic,
	// not a session validity signal.
	var name, avatarURL *string
	prefs := user.Profile{Theme: "system", Contrast: "system"}
	profile, err := h.users.GetProfile(ctx, authCtx.UserID)
	switch {
	case err == nil:
		prefs = *profile
		name = profile.DisplayName()
		if profile.AvatarFileID != nil {
			avatarURL = AvatarURL(ctx, h.files, h.backend, tenantCtx.Slug, authCtx.UserID, *profile.AvatarFileID)
		}
	case errors.Is(err, user.ErrProfileNotFound):
		// Leave name/avatarURL nil.
	default:
		log.Warn().Err(err).Str("user_id", authCtx.UserID).Msg("authme: profile lookup failed, omitting name/avatar")
	}

	// Cosmetic like the profile above, so a failure degrades to nil.
	member, err := h.members.GetMemberProfile(ctx, tenantCtx.Slug, authCtx.UserID)
	if err != nil && !errors.Is(err, role.ErrNotMember) {
		log.Warn().Err(err).Str("user_id", authCtx.UserID).Msg("authme: member profile lookup failed, omitting phone/title")
	}

	// Degrades to false: step 9 still rejects module routes with
	// mfa_setup_required, which the shell also routes to the wizard.
	setupRequired, err := h.auth.MFASetupRequired(ctx, tenantCtx.TenantID, authCtx)
	if err != nil {
		log.Warn().Err(err).Str("user_id", authCtx.UserID).Msg("authme: mfa setup check failed, reporting false")
	}

	// Degrades to this tenant's own minimum; the change-password answer
	// reports the minimum actually applied if it's higher.
	combinedMin, err := h.policies.CombinedMinLength(ctx, authCtx.UserID, tenantCtx.TenantID)
	if err != nil {
		log.Warn().Err(err).Str("user_id", authCtx.UserID).Msg("authme: combined password minimum lookup failed, reporting the tenant's")
		combinedMin = h.policies.MinLength(ctx, tenantCtx.TenantID)
	}

	l10nSettings, err := h.locales.Load(ctx, tenantCtx.TenantID)
	if err != nil {
		log.Warn().Err(err).Str("tenant_id", tenantCtx.TenantID).Msg("authme: tenant locale settings lookup failed, reporting platform defaults")
		l10nSettings = tenantl10n.Settings{
			DefaultLocale:    l10n.PlatformDefaultLocale,
			DefaultTimezone:  l10n.PlatformDefaultTimezone,
			AvailableLocales: h.locales.PlatformLocales(),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, meResponse{
		User: meUser{
			ID:               authCtx.UserID,
			Email:            u.Email,
			ContactID:        member.ContactID,
			Phone:            member.Phone,
			Title:            member.JobTitle,
			Name:             name,
			AvatarURL:        avatarURL,
			Roles:            authCtx.RolesLive,
			AMR:              authCtx.AMR,
			MFAVerifiedAt:    authCtx.MFAVerifiedAt,
			MFASetupRequired: setupRequired,

			PasswordChangeRequired: authCtx.PasswordChangeRequired,
			PasswordMinLength:      combinedMin,

			Theme:      prefs.Theme,
			Contrast:   prefs.Contrast,
			Locale:     prefs.Locale,
			Timezone:   prefs.Timezone,
			DateFormat: prefs.DateFormat,
		},
		Tenant: meTenant{
			ID:                tenantCtx.TenantID,
			Slug:              tenantCtx.Slug,
			Name:              tenantCtx.Name,
			Plan:              string(tenantCtx.Plan),
			DefaultLocale:     l10nSettings.DefaultLocale,
			DefaultTimezone:   l10nSettings.DefaultTimezone,
			AvailableLocales:  l10nSettings.AvailableLocales,
			PasswordMinLength: h.policies.MinLength(ctx, tenantCtx.TenantID),
		},
	})
}

// AvatarURL turns a user's stored avatar file id into a signed URL, or nil
// on any failure (missing files.Store/storage.Backend dependency, the file
// row having since been deleted, or a backend error) — an avatar is
// cosmetic, so callers degrade to no avatar rather than failing.
func AvatarURL(ctx context.Context, filesStore *files.Store, backend storage.Backend, tenantSlug, userID, fileID string) *string {
	if filesStore == nil || backend == nil {
		return nil
	}

	f, err := filesStore.GetByID(ctx, tenantSlug, fileID)
	if err != nil {
		if !errors.Is(err, files.ErrFileNotFound) {
			log.Warn().Err(err).Str("user_id", userID).Str("file_id", fileID).Msg("avatar file lookup failed")
		}
		return nil
	}
	// A soft-deleted file (authmeupdate.Handler's own validation already
	// rejects setting avatar_id to one — same rule enforced here for a
	// profile pointing at a file deleted after being set) has nothing
	// left to sign a working URL for.
	if f.DeletedAt != nil {
		return nil
	}

	url, err := backend.SignedURL(ctx, f.StorageKey, avatarURLExpiry)
	if err != nil {
		log.Warn().Err(err).Str("user_id", userID).Str("file_id", fileID).Msg("avatar signed URL generation failed")
		return nil
	}

	return &url
}
