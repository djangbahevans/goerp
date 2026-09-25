package engine

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authme"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/trace"
)

// /_meta/activity (record-activity.md §6): one record's feed, and posting
// and deleting comments on it. Every route first checks the caller can
// read the target record (§7).

const (
	activityDefaultLimit  = 20
	activityMaxLimit      = 100
	activityMaxBodyLength = 10000
)

type activityAuthor struct {
	ID        string  `json:"id"`
	Name      *string `json:"name"`
	AvatarURL *string `json:"avatar_url"`
}

type activityEntryResponse struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Body      *string         `json:"body,omitempty"`
	Deleted   *bool           `json:"deleted,omitempty"`
	Changes   jsontext.Value  `json:"changes,omitzero"`
	Activity  jsontext.Value  `json:"activity,omitzero"`
	Author    *activityAuthor `json:"author"`
	CreatedAt time.Time       `json:"created_at"`
}

type activityListMeta struct {
	Cursor  *string `json:"cursor"`
	HasMore bool    `json:"has_more"`
}

type activityCreateRequest struct {
	Model    string `json:"model"`
	RecordID string `json:"record_id"`
	Body     string `json:"body"`
}

// activityTarget resolves and validates the (model, record_id) a GET or
// POST names, writing the error response and returning false when the
// request can't proceed.
func (e *Engine) activityTarget(ctx context.Context, w http.ResponseWriter, authCtx *authcheck.AuthContext, tenantCtx *tenantresolve.TenantContext, modelName, recordID string) bool {
	if modelName == "" || recordID == "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "model and record_id are required")
		return false
	}
	if _, err := uuid.Parse(recordID); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "record_id must be a UUID")
		return false
	}

	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return false
	}
	_, _, md, ok := snap.ModelByName(modelName)
	if !ok {
		writeRouteError(w, http.StatusBadRequest, "model_not_found", "unknown model: "+modelName)
		return false
	}
	if md.Backend != "" {
		writeRouteError(w, http.StatusBadRequest, "activity_unsupported", modelName+" is "+string(md.Backend)+"-backed; activity feeds require a Postgres-backed model")
		return false
	}

	if !e.callerCanReadRecord(ctx, authCtx, tenantCtx, modelName, recordID) {
		writeRouteError(w, http.StatusForbidden, "permission_denied", "you do not have access to this record")
		return false
	}
	return true
}

// dispatchActivityListRoute is GET /_meta/activity's handler —
// ?model=&record_id=&cursor=&limit= pages one record's feed, newest first.
func (e *Engine) dispatchActivityListRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	q := r.URL.Query()
	cursor := q.Get("cursor")
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "malformed cursor")
			return
		}
	}
	limit := activityDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > activityMaxLimit {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer from 1 to 100")
			return
		}
		limit = n
	}

	ctx := r.Context()
	modelName, recordID := q.Get("model"), q.Get("record_id")
	if !e.activityTarget(ctx, w, authCtx, tenantCtx, modelName, recordID) {
		return
	}

	entries, hasMore, err := e.recordActivityStore.List(ctx, tenantCtx.Slug, modelName, recordID, cursor, limit)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "list activity failed")
		return
	}

	// The cursor is the last stored entry, taken before field-level
	// filtering drops any, so the next page continues after it.
	var nextCursor *string
	if hasMore {
		nextCursor = &entries[len(entries)-1].ID
	}

	readable := e.readableFieldFilter(authCtx, modelName)
	authors := e.newActivityAuthorResolver(tenantCtx.Slug)
	out := make([]activityEntryResponse, 0, len(entries))
	for i := range entries {
		entry := &entries[i]
		if entry.Kind == recordactivity.KindChange {
			changes, keep, err := filterChanges(entry.Changes, readable)
			if err != nil {
				writeRouteError(w, http.StatusInternalServerError, "internal_error", "list activity failed")
				return
			}
			if !keep {
				continue
			}
			entry.Changes = changes
		}
		out = append(out, activityEntryToResponse(entry, authors.resolve(ctx, entry.AuthorID)))
	}
	meta := activityListMeta{HasMore: hasMore, Cursor: nextCursor}
	writeJSON(w, http.StatusOK, map[string]any{"data": out, "meta": meta})
}

// dispatchActivityCreateRoute is POST /_meta/activity's handler — posts a
// plain-text comment authored by the caller. Commenting needs only read
// access to the record (record-activity.md §7).
func (e *Engine) dispatchActivityCreateRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	var body activityCreateRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return
	}
	text := strings.TrimSpace(body.Body)
	if text == "" || utf8.RuneCountInString(text) > activityMaxBodyLength {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "body must be 1 to 10000 characters")
		return
	}

	ctx := r.Context()
	if !e.activityTarget(ctx, w, authCtx, tenantCtx, body.Model, body.RecordID) {
		return
	}

	traceID := ""
	if sc := trace.SpanFromContext(ctx).SpanContext(); sc.HasTraceID() {
		traceID = sc.TraceID().String()
	}
	entry, err := e.recordActivityStore.CreateComment(ctx, tenantCtx.Slug, body.Model, body.RecordID, authCtx.UserID, text, requestIDFromContext(ctx), traceID)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "create comment failed")
		return
	}

	authors := e.newActivityAuthorResolver(tenantCtx.Slug)
	writeJSON(w, http.StatusCreated, activityEntryToResponse(entry, authors.resolve(ctx, entry.AuthorID)))
}

// dispatchActivityDeleteRoute is DELETE /_meta/activity/{id}'s handler —
// soft-deletes a comment. Only the comment's author may delete it, and
// only while they can still read the record; a repeat delete is a 204.
func (e *Engine) dispatchActivityDeleteRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	id := route.ParamsFromContext(r.Context())["id"]
	if id == "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return
	}

	ctx := r.Context()
	entry, err := e.recordActivityStore.Get(ctx, tenantCtx.Slug, id)
	if err != nil {
		if errors.Is(err, recordactivity.ErrNotFound) {
			writeRouteError(w, http.StatusNotFound, "not_found", "comment not found")
			return
		}
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "delete comment failed")
		return
	}
	if entry.Kind != recordactivity.KindComment {
		writeRouteError(w, http.StatusNotFound, "not_found", "comment not found")
		return
	}
	if !e.callerCanReadRecord(ctx, authCtx, tenantCtx, entry.Model, entry.RecordID) {
		writeRouteError(w, http.StatusForbidden, "permission_denied", "you do not have access to this record")
		return
	}
	if entry.AuthorID == nil || *entry.AuthorID != authCtx.UserID {
		writeRouteError(w, http.StatusForbidden, "not_author", "only a comment's author can delete it")
		return
	}

	if entry.DeletedAt == nil {
		if err := e.recordActivityStore.DeleteComment(ctx, tenantCtx.Slug, id); err != nil {
			writeRouteError(w, http.StatusInternalServerError, "internal_error", "delete comment failed")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func activityEntryToResponse(entry *recordactivity.Entry, author *activityAuthor) activityEntryResponse {
	resp := activityEntryResponse{
		ID:        entry.ID,
		Kind:      entry.Kind,
		Changes:   entry.Changes,
		Activity:  entry.Activity,
		Author:    author,
		CreatedAt: entry.CreatedAt,
	}
	if entry.Kind == recordactivity.KindComment {
		resp.Deleted = new(entry.DeletedAt != nil)
		if entry.DeletedAt == nil {
			resp.Body = entry.Body
		}
	}
	return resp
}

// activityAuthorResolver hydrates author ids to {id, name, avatar_url},
// looking each distinct author up once per response. A failed or missing
// profile degrades to a null name and avatar rather than failing the page.
type activityAuthorResolver struct {
	e          *Engine
	tenantSlug string
	seen       map[string]*activityAuthor
}

func (e *Engine) newActivityAuthorResolver(tenantSlug string) *activityAuthorResolver {
	return &activityAuthorResolver{e: e, tenantSlug: tenantSlug, seen: map[string]*activityAuthor{}}
}

func (a *activityAuthorResolver) resolve(ctx context.Context, authorID *string) *activityAuthor {
	if authorID == nil {
		return nil
	}
	if author, ok := a.seen[*authorID]; ok {
		return author
	}

	author := &activityAuthor{ID: *authorID}
	profile, err := a.e.userStore.GetProfile(ctx, *authorID)
	switch {
	case err == nil:
		author.Name = &profile.Name
		if profile.AvatarFileID != nil {
			author.AvatarURL = authme.AvatarURL(ctx, a.e.filesStore, a.e.storageBackend, a.tenantSlug, *authorID, *profile.AvatarFileID)
		}
	case errors.Is(err, user.ErrProfileNotFound):
	default:
		log.Warn().Err(err).Str("user_id", *authorID).Msg("dispatch activity: author profile lookup failed, omitting name/avatar")
	}
	a.seen[*authorID] = author
	return author
}

// readableFieldFilter reports whether authCtx's caller can read a field of
// modelName under its .Access() rule. A field with no read rule is readable.
// Fails closed on an unresolved registry.
func (e *Engine) readableFieldFilter(authCtx *authcheck.AuthContext, modelName string) func(field string) bool {
	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		return func(string) bool { return false }
	}
	fieldSec := snap.FieldSecRegistry()
	permReg := snap.PermissionRegistry()
	return func(field string) bool {
		if fieldSec == nil {
			return true
		}
		rule, ok := fieldSec.Rule(modelName, field)
		if !ok || rule.ReadPermission == "" {
			return true
		}
		return hasPermission(permReg, authCtx, rule.ReadPermission)
	}
}

// filterChanges removes every field the caller can't read from a change
// entry's changes (record-activity.md §7), whatever the field's
// OnDeniedRead behaviour: a masked value in a history entry still reveals
// that the field changed. keep is false when no readable field is left.
func filterChanges(raw jsontext.Value, readable func(field string) bool) (filtered jsontext.Value, keep bool, err error) {
	var changes []map[string]jsontext.Value
	if err := json.Unmarshal(raw, &changes); err != nil {
		return nil, false, err
	}
	kept := changes[:0]
	for _, c := range changes {
		var field string
		if err := json.Unmarshal(c["field"], &field); err != nil {
			return nil, false, err
		}
		if readable(field) {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 {
		return nil, false, nil
	}
	if len(kept) == len(changes) {
		return raw, true, nil
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}
