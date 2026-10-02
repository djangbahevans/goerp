package engine

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authme"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/rs/zerolog/log"
)

// /_meta/activity (record-activity.md §6): one record's feed, posting and
// deleting comments on it, and its followers. Every route first checks the
// caller can read the target record (§7).

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

// activityMention is one user a comment's mention tokens name, with
// their current name and email: name is null for a user with no profile
// name, and both are null for a user who no longer exists.
type activityMention struct {
	ID    string  `json:"id"`
	Name  *string `json:"name"`
	Email *string `json:"email"`
}

type activityEntryResponse struct {
	ID              string            `json:"id"`
	Kind            string            `json:"kind"`
	Body            *string           `json:"body,omitempty"`
	Mentions        []activityMention `json:"mentions,omitzero"`
	NotifyFollowers *bool             `json:"notify_followers,omitempty"`
	Deleted         *bool             `json:"deleted,omitempty"`
	Changes         jsontext.Value    `json:"changes,omitzero"`
	Activity        jsontext.Value    `json:"activity,omitzero"`
	Author          *activityAuthor   `json:"author"`
	CreatedAt       time.Time         `json:"created_at"`
}

type activityListMeta struct {
	Cursor  *string `json:"cursor"`
	HasMore bool    `json:"has_more"`
}

type activityCreateRequest struct {
	Model           string `json:"model"`
	RecordID        string `json:"record_id"`
	Body            string `json:"body"`
	NotifyFollowers bool   `json:"notify_followers"`
}

// activityTarget resolves and validates the (model, record_id) a GET or
// POST names, writing the error response and returning false when the
// request can't proceed.
func (e *Engine) activityTarget(ctx context.Context, w http.ResponseWriter, authCtx *authcheck.AuthContext, tenantCtx *tenantresolve.TenantContext, modelName, recordID string) bool {
	if modelName == "" || recordID == "" {
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_request", "model and record_id are required")
		return false
	}
	if _, err := uuid.Parse(recordID); err != nil {
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_request", "record_id must be a UUID")
		return false
	}

	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		httperr.Write(ctx, w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return false
	}
	_, _, md, ok := snap.ModelByName(modelName)
	if !ok {
		httperr.Write(ctx, w, http.StatusBadRequest, "model_not_found", "unknown model: "+modelName)
		return false
	}
	if md.Backend != "" {
		httperr.Write(ctx, w, http.StatusBadRequest, "activity_unsupported", modelName+" is "+string(md.Backend)+"-backed; activity feeds require a Postgres-backed model")
		return false
	}

	if !e.callerCanReadRecord(ctx, authCtx, tenantCtx, modelName, recordID) {
		httperr.Write(ctx, w, http.StatusForbidden, "permission_denied", "you do not have access to this record")
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
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	q := r.URL.Query()
	cursor := q.Get("cursor")
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed cursor")
			return
		}
	}
	limit := activityDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > activityMaxLimit {
			httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "limit must be an integer from 1 to 100")
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
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "list activity failed")
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
	users := e.newActivityUserResolver()
	out := make([]activityEntryResponse, 0, len(entries))
	for i := range entries {
		entry := &entries[i]
		if entry.Kind == recordactivity.KindChange {
			changes, keep, err := filterChanges(entry.Changes, readable)
			if err != nil {
				httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "list activity failed")
				return
			}
			if !keep {
				continue
			}
			entry.Changes = changes
		}
		out = append(out, activityEntryToResponse(ctx, entry, authors.resolve(ctx, entry.AuthorID), users))
	}
	meta := activityListMeta{HasMore: hasMore, Cursor: nextCursor}
	writeJSON(ctx, w, http.StatusOK, map[string]any{"data": out, "meta": meta})
}

// dispatchActivityCreateRoute is POST /_meta/activity's handler — posts a
// plain-text comment authored by the caller. Commenting needs only read
// access to the record (record-activity.md §7); every user the comment
// mentions must be able to read it too (§9). A comment that can notify
// anyone enqueues its notification job in the same transaction (§10).
func (e *Engine) dispatchActivityCreateRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	var body activityCreateRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return
	}
	text := strings.TrimSpace(body.Body)
	if text == "" || utf8.RuneCountInString(text) > activityMaxBodyLength {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "body must be 1 to 10000 characters")
		return
	}
	mentions := recordactivity.MentionIDs(text)
	if len(mentions) > recordactivity.MaxMentions {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "a comment can mention at most 20 users")
		return
	}

	ctx := r.Context()
	if !e.activityTarget(ctx, w, authCtx, tenantCtx, body.Model, body.RecordID) {
		return
	}
	unmentionable, err := e.unmentionableUsers(ctx, tenantCtx, body.Model, body.RecordID, authCtx.UserID, mentions)
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantCtx.Slug).Msg("dispatch activity: mention check failed")
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "create comment failed")
		return
	}
	if len(unmentionable) > 0 {
		httperr.WriteDetails(r.Context(), w, http.StatusBadRequest, "invalid_mention",
			"a mentioned user is not an active member who can read this record", map[string]any{"user_ids": unmentionable})
		return
	}

	traceID := httperr.TraceIDFromContext(ctx)
	comment := recordactivity.NewComment{
		Model: body.Model, RecordID: body.RecordID, AuthorID: authCtx.UserID, Body: text,
		NotifyFollowers: body.NotifyFollowers, RequestID: requestIDFromContext(ctx), TraceID: traceID,
	}
	var enqueue func(*sql.Tx, *recordactivity.Entry) error
	if e.txJobs != nil && commentMayNotify(comment, mentions) {
		enqueue = func(tx *sql.Tx, entry *recordactivity.Entry) error {
			args := jobqueue.RecordCommentNotifyArgs{TenantID: tenantCtx.TenantID, TenantSlug: tenantCtx.Slug, EntryID: entry.ID}
			_, err := e.txJobs.InsertTx(ctx, tx, args, nil)
			return err
		}
	}
	entry, err := e.recordActivityStore.CreateComment(ctx, tenantCtx.Slug, comment, enqueue)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "create comment failed")
		return
	}

	authors := e.newActivityAuthorResolver(tenantCtx.Slug)
	writeJSON(ctx, w, http.StatusCreated, activityEntryToResponse(ctx, entry, authors.resolve(ctx, entry.AuthorID), e.newActivityUserResolver()))
}

// dispatchActivityDeleteRoute is DELETE /_meta/activity/{id}'s handler —
// soft-deletes a comment. Only the comment's author may delete it, and
// only while they can still read the record; a repeat delete is a 204.
func (e *Engine) dispatchActivityDeleteRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	id := route.ParamsFromContext(r.Context())["id"]
	if id == "" {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return
	}

	ctx := r.Context()
	entry, err := e.recordActivityStore.Get(ctx, tenantCtx.Slug, id)
	if err != nil {
		if errors.Is(err, recordactivity.ErrNotFound) {
			httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "comment not found")
			return
		}
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "delete comment failed")
		return
	}
	if entry.Kind != recordactivity.KindComment {
		httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "comment not found")
		return
	}
	if !e.callerCanReadRecord(ctx, authCtx, tenantCtx, entry.Model, entry.RecordID) {
		httperr.Write(r.Context(), w, http.StatusForbidden, "permission_denied", "you do not have access to this record")
		return
	}
	if entry.AuthorID == nil || *entry.AuthorID != authCtx.UserID {
		httperr.Write(r.Context(), w, http.StatusForbidden, "not_author", "only a comment's author can delete it")
		return
	}

	if entry.DeletedAt == nil {
		if err := e.recordActivityStore.DeleteComment(ctx, tenantCtx.Slug, id); err != nil {
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "delete comment failed")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

type activityFollowerResponse struct {
	User      *activityAuthor `json:"user"`
	CreatedAt time.Time       `json:"created_at"`
}

type activityFollowersMeta struct {
	Following bool `json:"following"`
}

type activityFollowRequest struct {
	Model    string `json:"model"`
	RecordID string `json:"record_id"`
}

// dispatchActivityFollowersListRoute is GET /_meta/activity/followers's
// handler — ?model=&record_id= lists the record's followers, oldest first,
// and whether the caller is one of them. Rows are listed as stored, so a
// follower who has lost read access shows until the notification job
// removes them (record-activity.md §8).
func (e *Engine) dispatchActivityFollowersListRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	ctx := r.Context()
	q := r.URL.Query()
	modelName, recordID := q.Get("model"), q.Get("record_id")
	if !e.activityTarget(ctx, w, authCtx, tenantCtx, modelName, recordID) {
		return
	}

	followers, err := e.recordActivityStore.ListFollowers(ctx, tenantCtx.Slug, modelName, recordID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "list followers failed")
		return
	}

	users := e.newActivityAuthorResolver(tenantCtx.Slug)
	out := make([]activityFollowerResponse, len(followers))
	meta := activityFollowersMeta{}
	for i := range followers {
		f := &followers[i]
		out[i] = activityFollowerResponse{User: users.resolve(ctx, &f.UserID), CreatedAt: f.CreatedAt}
		if f.UserID == authCtx.UserID {
			meta.Following = true
		}
	}
	writeJSON(ctx, w, http.StatusOK, map[string]any{"data": out, "meta": meta})
}

func (e *Engine) dispatchActivityFollowRoute(w http.ResponseWriter, r *http.Request) {
	e.dispatchActivityFollowChange(w, r, e.recordActivityStore.Follow, "follow failed")
}

func (e *Engine) dispatchActivityUnfollowRoute(w http.ResponseWriter, r *http.Request) {
	e.dispatchActivityFollowChange(w, r, e.recordActivityStore.Unfollow, "unfollow failed")
}

// dispatchActivityFollowChange applies change to the caller's follow of
// the record the {model, record_id} body names. Both directions act on the
// caller only: there is no way to follow or unfollow for someone else.
func (e *Engine) dispatchActivityFollowChange(w http.ResponseWriter, r *http.Request, change func(ctx context.Context, tenantSlug, model, recordID, userID string) error, failure string) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	var body activityFollowRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return
	}

	ctx := r.Context()
	if !e.activityTarget(ctx, w, authCtx, tenantCtx, body.Model, body.RecordID) {
		return
	}

	if err := change(ctx, tenantCtx.Slug, body.Model, body.RecordID, authCtx.UserID); err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", failure)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unmentionableUsers returns the ids in mentions, other than the author's
// own, of users who can't be mentioned on the record: anyone who isn't an
// active tenant member able to read it (record-activity.md §9).
func (e *Engine) unmentionableUsers(ctx context.Context, tenantCtx *tenantresolve.TenantContext, modelName, recordID, authorID string, mentions []string) ([]string, error) {
	var denied []string
	for _, id := range mentions {
		if id == authorID {
			continue
		}
		ok, err := e.userCanReadRecord(ctx, tenantCtx, id, modelName, recordID)
		if err != nil {
			return nil, err
		}
		if !ok {
			denied = append(denied, id)
		}
	}
	return denied, nil
}

// commentMayNotify reports whether c can notify anyone: it is a message
// to followers, or mentions someone other than its author.
func commentMayNotify(c recordactivity.NewComment, mentions []string) bool {
	return c.NotifyFollowers || slices.ContainsFunc(mentions, func(id string) bool { return id != c.AuthorID })
}

func activityEntryToResponse(ctx context.Context, entry *recordactivity.Entry, author *activityAuthor, users *activityUserResolver) activityEntryResponse {
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
		resp.NotifyFollowers = new(entry.NotifyFollowers)
		resp.Mentions = []activityMention{}
		if entry.DeletedAt == nil && entry.Body != nil {
			resp.Body = entry.Body
			for _, id := range recordactivity.MentionIDs(*entry.Body) {
				resp.Mentions = append(resp.Mentions, users.mention(ctx, id))
			}
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
		author.Name = profile.DisplayName()
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

// activityUserResolver looks up the users mention tokens name, each
// distinct user once.
type activityUserResolver struct {
	e    *Engine
	seen map[string]activityMention
}

func (e *Engine) newActivityUserResolver() *activityUserResolver {
	return &activityUserResolver{e: e, seen: map[string]activityMention{}}
}

// lookup returns userID's current name and email; both are nil for a
// user who no longer exists. A failed lookup is an error and isn't
// remembered, so a later call tries again.
func (u *activityUserResolver) lookup(ctx context.Context, userID string) (activityMention, error) {
	if m, ok := u.seen[userID]; ok {
		return m, nil
	}
	m := activityMention{ID: userID}
	usr, err := u.e.userStore.GetByID(ctx, userID)
	switch {
	case errors.Is(err, user.ErrUserNotFound):
	case err != nil:
		return m, fmt.Errorf("load mentioned user: %w", err)
	case usr.Status != user.StatusDeleted:
		m.Email = &usr.Email
		profile, err := u.e.userStore.GetProfile(ctx, userID)
		switch {
		case err == nil:
			m.Name = profile.DisplayName()
		case !errors.Is(err, user.ErrProfileNotFound):
			return m, fmt.Errorf("load mentioned user's profile: %w", err)
		}
	}
	u.seen[userID] = m
	return m, nil
}

// mention is lookup for a feed response, which degrades a failed lookup
// to null fields rather than failing the page.
func (u *activityUserResolver) mention(ctx context.Context, userID string) activityMention {
	m, err := u.lookup(ctx, userID)
	if err != nil {
		log.Warn().Err(err).Str("user_id", userID).Msg("dispatch activity: mentioned user lookup failed, omitting name/email")
	}
	return m
}

// label is how a mention of userID reads in text after its "@": their
// name, else their email's local part, else "Unknown user" for a user who
// no longer exists (record-activity.md §9).
func (u *activityUserResolver) label(ctx context.Context, userID string) (string, error) {
	m, err := u.lookup(ctx, userID)
	switch {
	case err != nil:
		return "", err
	case m.Name != nil:
		return *m.Name, nil
	case m.Email != nil:
		local, _, _ := strings.Cut(*m.Email, "@")
		return local, nil
	default:
		return "Unknown user", nil
	}
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
