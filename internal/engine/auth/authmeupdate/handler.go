// Package authmeupdate implements PATCH /auth/me (shell-ux.md §4.1) — the
// self-service profile save the /settings/profile page's "Save changes"
// button calls. Same tenant/auth resolution pattern as authme.Handler.
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
	"net/http"
	"strings"

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
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, users *user.Store, filesStore *files.Store) *Handler {
	return &Handler{tenants: tenants, auth: auth, users: users, files: filesStore}
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

// AvatarID has three states, matching user.Store.ReplaceProfile: the key
// absent from the JSON body (nil) leaves the avatar untouched; present as
// "" clears it; present as a real file id sets it. FileField's "remove"
// action must be able to reach the "" case — without it, clearing an
// avatar in the UI and saving would be indistinguishable from never
// touching the avatar field at all.
type updateRequest struct {
	Name     string  `json:"name"`
	AvatarID *string `json:"avatar_id"`
}

type updateResponse struct {
	Name string `json:"name"`
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

	var req updateRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", `"name" is required`)
		return
	}

	// A real avatar_id (not the "" clear sentinel) must reference a real,
	// non-deleted file already uploaded to this tenant (via POST
	// /storage/upload) — otherwise any authenticated user could set
	// avatar_id to an arbitrary UUID and have GET /auth/me attempt to
	// resolve and serve it.
	if req.AvatarID != nil && *req.AvatarID != "" {
		if h.files == nil {
			log.Warn().Str("user_id", authCtx.UserID).Msg("authmeupdate: no files.Store configured, rejecting avatar_id")
			writeJSONError(w, http.StatusServiceUnavailable, "storage_unavailable", "avatar updates are unavailable right now")
			return
		}
		f, err := h.files.GetByID(ctx, tenantCtx.Slug, *req.AvatarID)
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

	oldAvatarID, err := h.users.ReplaceProfile(ctx, authCtx.UserID, name, req.AvatarID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "update failed")
		return
	}

	// Best-effort: the profile save already succeeded, so a cleanup
	// failure here only leaves the old file un-marked, not a request
	// failure — object-storage-guide.md §10's "Manual deletion" is
	// explicitly the same best-effort shape (the actual storage object is
	// removed by a separate async sweep, not synchronously here). Covers
	// both a real replacement and a clear: for a clear, req.AvatarID
	// points at "", which is never equal to a real stored file id, so
	// oldAvatarID still gets marked deleted correctly.
	if oldAvatarID != nil && req.AvatarID != nil && *oldAvatarID != *req.AvatarID {
		if err := h.files.MarkDeleted(ctx, tenantCtx.Slug, *oldAvatarID); err != nil {
			log.Warn().Err(err).Str("user_id", authCtx.UserID).Str("file_id", *oldAvatarID).Msg("authmeupdate: failed to mark replaced avatar deleted")
		}
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, updateResponse{Name: name})
}
