package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/scheduledactivity"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	sdkmodel "github.com/djangbahevans/goerp/sdk/go/model"
	"go.opentelemetry.io/otel/trace"
)

// /_meta/scheduled-activities (scheduled-activities.md §5): a record's open
// activities, the caller's own across records, and scheduling, editing,
// completing and cancelling them. The record decides access (§6): every
// route checks the caller can read the activity's record before anything
// else.

const (
	scheduledActivityMineDefaultLimit = 50
	scheduledActivityMineMaxLimit     = 200
	scheduledActivityMaxSummary       = 200
	scheduledActivityMaxText          = 10000
)

type scheduledActivityResponse struct {
	ID        string          `json:"id"`
	Model     string          `json:"model"`
	RecordID  string          `json:"record_id"`
	Type      string          `json:"type"`
	Summary   string          `json:"summary"`
	Note      *string         `json:"note"`
	DueDate   string          `json:"due_date"`
	Assignee  *activityAuthor `json:"assignee"`
	CreatedBy *activityAuthor `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	DoneAt    *time.Time      `json:"done_at"`
	DoneBy    *activityAuthor `json:"done_by"`
	Feedback  *string         `json:"feedback"`
}

type myScheduledActivityResponse struct {
	scheduledActivityResponse `json:",inline"`
	RecordName                *string `json:"record_name"`
}

type scheduledActivityCreateRequest struct {
	Model      string  `json:"model"`
	RecordID   string  `json:"record_id"`
	Type       string  `json:"type"`
	Summary    string  `json:"summary"`
	Note       *string `json:"note"`
	DueDate    string  `json:"due_date"`
	AssigneeID *string `json:"assignee_id"`
}

// scheduledActivityPatchRequest's Note is raw so an explicit null (clear
// the note) is told apart from an absent key (leave it).
type scheduledActivityPatchRequest struct {
	Type       *string        `json:"type"`
	Summary    *string        `json:"summary"`
	Note       jsontext.Value `json:"note,omitzero"`
	DueDate    *string        `json:"due_date"`
	AssigneeID *string        `json:"assignee_id"`
}

type scheduledActivityDoneRequest struct {
	Feedback *string `json:"feedback"`
}

// dispatchScheduledActivityListRoute is GET /_meta/scheduled-activities'
// handler — ?model=&record_id= lists the record's open activities, soonest
// due first. Not paged.
func (e *Engine) dispatchScheduledActivityListRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := requestContexts(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	modelName, recordID := q.Get("model"), q.Get("record_id")
	if !e.activityTarget(ctx, w, authCtx, tenantCtx, modelName, recordID) {
		return
	}

	activities, err := e.scheduledActivityStore.ListOpenForRecord(ctx, tenantCtx.Slug, modelName, recordID)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "list scheduled activities failed")
		return
	}
	users := e.newActivityAuthorResolver(tenantCtx.Slug)
	out := make([]scheduledActivityResponse, len(activities))
	for i := range activities {
		out[i] = scheduledActivityToResponse(ctx, &activities[i], users)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// dispatchScheduledActivityMineRoute is GET /_meta/scheduled-activities/mine's
// handler — the caller's open activities across records, cursor-paged by
// (due_date, id). Each page's records are read as the caller in one batch
// per model; activities whose record comes back empty are left out, and
// the rest carry the record's display name from that read.
func (e *Engine) dispatchScheduledActivityMineRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := requestContexts(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	var cursor *scheduledactivity.Cursor
	if raw := q.Get("cursor"); raw != "" {
		c, err := decodeScheduledActivityCursor(raw)
		if err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "malformed cursor")
			return
		}
		cursor = c
	}
	limit := scheduledActivityMineDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > scheduledActivityMineMaxLimit {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer from 1 to 200")
			return
		}
		limit = n
	}

	ctx := r.Context()
	activities, hasMore, err := e.scheduledActivityStore.ListOpenForAssignee(ctx, tenantCtx.Slug, authCtx.UserID, cursor, limit)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "list scheduled activities failed")
		return
	}

	// The cursor is the last stored activity, taken before unreadable
	// records drop any, so the next page continues after it.
	var nextCursor *string
	if hasMore {
		last := activities[len(activities)-1]
		nextCursor = new(encodeScheduledActivityCursor(last.DueDate, last.ID))
	}

	recordIDs := map[string][]string{}
	for _, a := range activities {
		recordIDs[a.Model] = append(recordIDs[a.Model], a.RecordID)
	}
	names := map[string]map[string]*string{}
	for modelName, ids := range recordIDs {
		names[modelName] = e.readableRecordNames(ctx, authCtx, tenantCtx, modelName, ids)
	}

	users := e.newActivityAuthorResolver(tenantCtx.Slug)
	out := make([]myScheduledActivityResponse, 0, len(activities))
	for i := range activities {
		a := &activities[i]
		name, readable := names[a.Model][a.RecordID]
		if !readable {
			continue
		}
		out = append(out, myScheduledActivityResponse{scheduledActivityToResponse(ctx, a, users), name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out, "meta": activityListMeta{Cursor: nextCursor, HasMore: hasMore}})
}

// dispatchScheduledActivityCreateRoute is POST /_meta/scheduled-activities'
// handler — schedules an open activity created by the caller and assigned
// to assignee_id, the caller by default.
func (e *Engine) dispatchScheduledActivityCreateRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := requestContexts(w, r)
	if !ok {
		return
	}
	var body scheduledActivityCreateRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return
	}
	summary, msg := validSummary(body.Summary)
	if msg != "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	var note *string
	if body.Note != nil {
		if note, msg = optionalText("note", *body.Note); msg != "" {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
	}
	if msg := validDueDate(body.DueDate); msg != "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	assigneeID := authCtx.UserID
	if body.AssigneeID != nil {
		if _, err := uuid.Parse(*body.AssigneeID); err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "assignee_id must be a UUID")
			return
		}
		assigneeID = *body.AssigneeID
	}
	if !scheduledactivity.ValidType(body.Type) {
		writeRouteError(w, http.StatusBadRequest, "invalid_type", "type must be one of "+strings.Join(scheduledactivity.Types, ", "))
		return
	}

	ctx := r.Context()
	if !e.activityTarget(ctx, w, authCtx, tenantCtx, body.Model, body.RecordID) {
		return
	}
	if !e.checkAssignee(ctx, w, authCtx, tenantCtx, assigneeID, body.Model, body.RecordID) {
		return
	}

	a, err := e.scheduledActivityStore.Create(ctx, tenantCtx.Slug, scheduledactivity.NewActivity{
		Model:      body.Model,
		RecordID:   body.RecordID,
		Type:       body.Type,
		Summary:    summary,
		Note:       note,
		DueDate:    body.DueDate,
		AssigneeID: assigneeID,
		CreatedBy:  authCtx.UserID,
	})
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "create scheduled activity failed")
		return
	}
	writeJSON(w, http.StatusCreated, scheduledActivityToResponse(ctx, a, e.newActivityAuthorResolver(tenantCtx.Slug)))
}

// dispatchScheduledActivityUpdateRoute is PATCH
// /_meta/scheduled-activities/{id}'s handler — edits an open activity's
// type, summary, note, due date or assignee.
func (e *Engine) dispatchScheduledActivityUpdateRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := requestContexts(w, r)
	if !ok {
		return
	}
	var body scheduledActivityPatchRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
		return
	}
	var u scheduledactivity.Update
	if body.Summary != nil {
		summary, msg := validSummary(*body.Summary)
		if msg != "" {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
		u.Summary = &summary
	}
	if body.Note != nil {
		var note *string
		if err := json.Unmarshal(body.Note, &note); err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "note must be a string or null")
			return
		}
		if note != nil {
			var msg string
			if note, msg = optionalText("note", *note); msg != "" {
				writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
				return
			}
		}
		u.Note, u.ClearNote = note, note == nil
	}
	if body.DueDate != nil {
		if msg := validDueDate(*body.DueDate); msg != "" {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
		u.DueDate = body.DueDate
	}
	if body.AssigneeID != nil {
		if _, err := uuid.Parse(*body.AssigneeID); err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "assignee_id must be a UUID")
			return
		}
		u.AssigneeID = body.AssigneeID
	}
	if body.Type != nil {
		if !scheduledactivity.ValidType(*body.Type) {
			writeRouteError(w, http.StatusBadRequest, "invalid_type", "type must be one of "+strings.Join(scheduledactivity.Types, ", "))
			return
		}
		u.Type = body.Type
	}

	ctx := r.Context()
	a := e.openScheduledActivityForParticipant(ctx, w, authCtx, tenantCtx)
	if a == nil {
		return
	}
	if u.AssigneeID != nil && *u.AssigneeID != a.AssigneeID {
		if !e.checkAssignee(ctx, w, authCtx, tenantCtx, *u.AssigneeID, a.Model, a.RecordID) {
			return
		}
	}

	updated, err := e.scheduledActivityStore.Update(ctx, tenantCtx.Slug, a.ID, u)
	if err != nil {
		writeScheduledActivityStoreError(w, err, "update scheduled activity failed")
		return
	}
	writeJSON(w, http.StatusOK, scheduledActivityToResponse(ctx, updated, e.newActivityAuthorResolver(tenantCtx.Slug)))
}

// dispatchScheduledActivityDoneRoute is POST
// /_meta/scheduled-activities/{id}/done's handler — marks an open activity
// done with optional feedback, writing its activity_done feed entry in the
// same transaction (record-activity.md §1).
func (e *Engine) dispatchScheduledActivityDoneRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := requestContexts(w, r)
	if !ok {
		return
	}
	var body scheduledActivityDoneRequest
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "request body could not be read")
		return
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object")
			return
		}
	}
	var feedback *string
	if body.Feedback != nil {
		var msg string
		if feedback, msg = optionalText("feedback", *body.Feedback); msg != "" {
			writeRouteError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
	}

	ctx := r.Context()
	a := e.openScheduledActivityForParticipant(ctx, w, authCtx, tenantCtx)
	if a == nil {
		return
	}

	traceID := ""
	if sc := trace.SpanFromContext(ctx).SpanContext(); sc.HasTraceID() {
		traceID = sc.TraceID().String()
	}
	done, err := e.scheduledActivityStore.MarkDone(ctx, tenantCtx.Slug, a.ID, authCtx.UserID, feedback, requestIDFromContext(ctx), traceID)
	if err != nil {
		writeScheduledActivityStoreError(w, err, "mark scheduled activity done failed")
		return
	}
	writeJSON(w, http.StatusOK, scheduledActivityToResponse(ctx, done, e.newActivityAuthorResolver(tenantCtx.Slug)))
}

// dispatchScheduledActivityCancelRoute is DELETE
// /_meta/scheduled-activities/{id}'s handler — deletes an open activity,
// leaving no row and no feed entry.
func (e *Engine) dispatchScheduledActivityCancelRoute(w http.ResponseWriter, r *http.Request) {
	authCtx, tenantCtx, ok := requestContexts(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	a := e.openScheduledActivityForParticipant(ctx, w, authCtx, tenantCtx)
	if a == nil {
		return
	}
	if err := e.scheduledActivityStore.Cancel(ctx, tenantCtx.Slug, a.ID); err != nil {
		writeScheduledActivityStoreError(w, err, "cancel scheduled activity failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func requestContexts(w http.ResponseWriter, r *http.Request) (*authcheck.AuthContext, *tenantresolve.TenantContext, bool) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return nil, nil, false
	}
	return authCtx, tenantCtx, true
}

// openScheduledActivityForParticipant loads the {id} path parameter's
// activity for a PATCH, done or DELETE and runs scheduled-activities.md §6's
// checks in order: the caller can read its record, is its creator or
// assignee, and it is still open. Writes the error response and returns
// nil when the request can't proceed.
func (e *Engine) openScheduledActivityForParticipant(ctx context.Context, w http.ResponseWriter, authCtx *authcheck.AuthContext, tenantCtx *tenantresolve.TenantContext) *scheduledactivity.Activity {
	id := route.ParamsFromContext(ctx)["id"]
	if _, err := uuid.Parse(id); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_path_param", "id path parameter must be a UUID")
		return nil
	}
	a, err := e.scheduledActivityStore.Get(ctx, tenantCtx.Slug, id)
	if err != nil {
		writeScheduledActivityStoreError(w, err, "load scheduled activity failed")
		return nil
	}
	if !e.callerCanReadRecord(ctx, authCtx, tenantCtx, a.Model, a.RecordID) {
		writeRouteError(w, http.StatusForbidden, "permission_denied", "you do not have access to this record")
		return nil
	}
	if authCtx.UserID != a.CreatedBy && authCtx.UserID != a.AssigneeID {
		writeRouteError(w, http.StatusForbidden, "not_participant", "only the activity's creator or assignee can change it")
		return nil
	}
	if a.DoneAt != nil {
		writeRouteError(w, http.StatusConflict, "activity_done", "the activity is already done")
		return nil
	}
	return a
}

// checkAssignee reports whether assigneeID is an active tenant member who
// can read the record, reading it with their permissions
// (scheduled-activities.md §6). The caller has already been checked.
// Writes the error response when not.
func (e *Engine) checkAssignee(ctx context.Context, w http.ResponseWriter, authCtx *authcheck.AuthContext, tenantCtx *tenantresolve.TenantContext, assigneeID, modelName, recordID string) bool {
	if assigneeID == authCtx.UserID {
		return true
	}
	canRead, err := e.userCanReadRecord(ctx, tenantCtx, assigneeID, modelName, recordID)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "check assignee failed")
		return false
	}
	if !canRead {
		writeRouteError(w, http.StatusBadRequest, "invalid_assignee", "the assignee must be an active member of the tenant who can read the record")
		return false
	}
	return true
}

func writeScheduledActivityStoreError(w http.ResponseWriter, err error, internalMsg string) {
	switch {
	case errors.Is(err, scheduledactivity.ErrNotFound):
		writeRouteError(w, http.StatusNotFound, "not_found", "scheduled activity not found")
	case errors.Is(err, scheduledactivity.ErrDone):
		writeRouteError(w, http.StatusConflict, "activity_done", "the activity is already done")
	default:
		writeRouteError(w, http.StatusInternalServerError, "internal_error", internalMsg)
	}
}

// readableRecordNames reads ids of modelName as the caller and returns each
// readable record's display name, keyed by id. An id the read doesn't
// return is absent; a model that isn't a registered Postgres-backed one
// returns no ids.
func (e *Engine) readableRecordNames(ctx context.Context, authCtx *authcheck.AuthContext, tenantCtx *tenantresolve.TenantContext, modelName string, ids []string) map[string]*string {
	names := map[string]*string{}
	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		return names
	}
	_, mod, md, ok := snap.ModelByName(modelName)
	if !ok || md.Backend != "" {
		return names
	}
	pk, label := recordLabelFields(md, modelName, mod.Manifest.Views)
	records, ok := e.readRecordsAs(ctx, tenantCtx, authCtx.UserID, authCtx.PermissionSet, modelName, ids, []string{pk, label})
	if !ok {
		return names
	}
	for _, rec := range records {
		var name *string
		if v := rec[label]; v != nil {
			name = new(fmt.Sprint(v))
		}
		names[fmt.Sprint(rec[pk])] = name
	}
	return names
}

// recordLabelFields returns md's primary key field and the field holding
// its records' display name, by manifest-spec.md §8b "How labelField is
// determined": the default list view's label_field, then its first
// primary column, then the model's .Primary() field, then the first of
// display_name, name and title it declares, then the primary key. The
// default list view is the owning module's first list view of the model;
// a view field the model doesn't declare is skipped.
func recordLabelFields(md sdkmodel.ModelDeclaration, modelName string, views []manifest.View) (pk, label string) {
	declared := map[string]bool{}
	var primary string
	for _, f := range md.Fields {
		declared[f.Name] = true
		if f.Def.IsPrimaryKey && pk == "" {
			pk = f.Name
		}
		if f.Def.IsPrimary && primary == "" {
			primary = f.Name
		}
	}

	candidates := []string{}
	if i := slices.IndexFunc(views, func(v manifest.View) bool { return v.Type == "list" && v.Resource == modelName }); i >= 0 {
		view := views[i]
		candidates = append(candidates, view.LabelField)
		if j := slices.IndexFunc(view.Columns, func(c manifest.ListColumn) bool { return c.Primary }); j >= 0 {
			candidates = append(candidates, view.Columns[j].Field)
		}
	}
	candidates = append(candidates, primary, "display_name", "name", "title")
	for _, c := range candidates {
		if c != "" && declared[c] {
			return pk, c
		}
	}
	return pk, pk
}

func scheduledActivityToResponse(ctx context.Context, a *scheduledactivity.Activity, users *activityAuthorResolver) scheduledActivityResponse {
	return scheduledActivityResponse{
		ID:        a.ID,
		Model:     a.Model,
		RecordID:  a.RecordID,
		Type:      a.Type,
		Summary:   a.Summary,
		Note:      a.Note,
		DueDate:   a.DueDate,
		Assignee:  users.resolve(ctx, &a.AssigneeID),
		CreatedBy: users.resolve(ctx, &a.CreatedBy),
		CreatedAt: a.CreatedAt,
		DoneAt:    a.DoneAt,
		DoneBy:    users.resolve(ctx, a.DoneBy),
		Feedback:  a.Feedback,
	}
}

// validSummary trims s and checks it is 1–200 characters, returning the
// trimmed summary or an error message.
func validSummary(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > scheduledActivityMaxSummary {
		return "", "summary must be 1 to 200 characters"
	}
	return s, ""
}

// optionalText trims s and checks it is at most 10,000 characters; an
// empty result is nil.
func optionalText(field, s string) (*string, string) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > scheduledActivityMaxText {
		return nil, field + " must be at most 10000 characters"
	}
	if s == "" {
		return nil, ""
	}
	return &s, ""
}

func validDueDate(s string) string {
	if _, err := time.Parse(time.DateOnly, s); err != nil {
		return "due_date must be a date in YYYY-MM-DD form"
	}
	return ""
}

func encodeScheduledActivityCursor(dueDate, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(dueDate + "," + id))
}

func decodeScheduledActivityCursor(raw string) (*scheduledactivity.Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	dueDate, id, ok := strings.Cut(string(b), ",")
	if !ok {
		return nil, errors.New("cursor has no separator")
	}
	if _, err := time.Parse(time.DateOnly, dueDate); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, err
	}
	return &scheduledactivity.Cursor{DueDate: dueDate, ID: id}, nil
}
