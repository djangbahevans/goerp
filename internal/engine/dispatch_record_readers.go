package engine

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/djangbahevans/goerp/internal/engine/auth/authme"
	"github.com/rs/zerolog/log"
)

const (
	recordReadersDefaultLimit = 8
	recordReadersMaxLimit     = 20
	recordReadersMaxQuery     = 100
	// recordReadersMaxChecked bounds the per-user record reads one request
	// can cost: a query that must skip more non-readers than this returns
	// fewer results rather than scanning further.
	recordReadersMaxChecked = 50
)

type recordReaderResponse struct {
	ID        string  `json:"id"`
	Name      *string `json:"name"`
	Email     string  `json:"email"`
	AvatarURL *string `json:"avatar_url"`
}

// dispatchRecordReadersRoute is GET /_meta/record-readers' handler
// (record-activity.md §6) — ?model=&record_id=&q=&limit=&exclude_self=,
// the one user lookup behind every per-record picker. Members matching q are
// filtered in SQL, then read-checked as themselves in name order until
// limit readers or recordReadersMaxChecked matches.
func (e *Engine) dispatchRecordReadersRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	q := r.URL.Query()
	search := q.Get("q")
	if !utf8.ValidString(search) || strings.ContainsRune(search, 0) {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "q must be valid UTF-8 text")
		return
	}
	if utf8.RuneCountInString(search) > recordReadersMaxQuery {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "q must be at most 100 characters")
		return
	}
	limit := recordReadersDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > recordReadersMaxLimit {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer from 1 to 20")
			return
		}
		limit = n
	}
	excludeSelf := false
	if raw := q.Get("exclude_self"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "exclude_self must be true or false")
			return
		}
		excludeSelf = b
	}

	ctx := r.Context()
	modelName, recordID := q.Get("model"), q.Get("record_id")
	if !e.activityTarget(ctx, w, authCtx, tenantCtx, modelName, recordID) {
		return
	}

	excludeID := ""
	if excludeSelf {
		excludeID = authCtx.UserID
	}
	candidates, err := e.roleStore.SearchMembers(ctx, tenantCtx.Slug, search, excludeID, recordReadersMaxChecked)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "search record readers failed")
		return
	}

	out := make([]recordReaderResponse, 0, limit)
	for _, m := range candidates {
		if len(out) == limit {
			break
		}
		// activityTarget has already checked the caller can read it.
		if m.ID != authCtx.UserID {
			canRead, err := e.userCanReadRecord(ctx, tenantCtx, m.ID, modelName, recordID)
			if err != nil {
				log.Warn().Err(err).Str("user_id", m.ID).Msg("dispatch record readers: read check failed, omitting user")
				continue
			}
			if !canRead {
				continue
			}
		}
		reader := recordReaderResponse{ID: m.ID, Name: m.Name, Email: m.Email}
		if m.AvatarFileID != nil {
			reader.AvatarURL = authme.AvatarURL(ctx, e.filesStore, e.storageBackend, tenantCtx.Slug, m.ID, *m.AvatarFileID)
		}
		out = append(out, reader)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
