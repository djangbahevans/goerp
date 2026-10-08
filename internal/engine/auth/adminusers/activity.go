package adminusers

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

const (
	sourceAuth = "auth"
	sourceData = "data"
)

// ModelForTable maps an audit_log.table_name to the qualified name of the
// installed model that owns the table.
type ModelForTable func(table string) (string, bool)

type activityCursor struct {
	OccurredAt time.Time
	ID         string
}

type activityFilter struct {
	UserID string
	After  *activityCursor
	Limit  int
}

type personJSON struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

type recordJSON struct {
	Model *string `json:"model"`
	ID    string  `json:"id"`
}

// activityJSON carries account activity metadata; data entries disclose changed field
// names without old or new values.
type activityJSON struct {
	ID            string          `json:"id"`
	Source        string          `json:"source"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Action        string          `json:"action"`
	Success       bool            `json:"success"`
	FailureReason *string         `json:"failure_reason"`
	Actor         *personJSON     `json:"actor"`
	User          *personJSON     `json:"user"`
	Record        *recordJSON     `json:"record"`
	ChangedFields *[]string       `json:"changed_fields"`
	IPAddress     *string         `json:"ip_address"`
	UserAgent     *string         `json:"user_agent"`
	Metadata      *jsontext.Value `json:"metadata"`

	table string
}

func (a activityJSON) cursor() activityCursor {
	return activityCursor{OccurredAt: a.OccurredAt, ID: a.ID}
}

// newerFirst orders entries by (occurred_at, id) descending. Both sources
// use uuidv7 ids in canonical lowercase form, so comparing the strings
// matches Postgres's uuid ordering.
func newerFirst(a, b activityJSON) int {
	if c := b.OccurredAt.Compare(a.OccurredAt); c != 0 {
		return c
	}
	return cmp.Compare(b.ID, a.ID)
}

func encodeActivityCursor(c activityCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.OccurredAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID))
}

func decodeActivityCursor(raw string) (*activityCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return nil, errors.New("missing separator")
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, err
	}
	return &activityCursor{OccurredAt: at, ID: id}, nil
}

func person(id, name *string) *personJSON {
	if id == nil {
		return nil
	}
	return &personJSON{ID: *id, Name: name}
}

// afterClause is the keyset condition for rows sorting after f.After in
// newest-first order, with its arguments numbered from next.
func afterClause(f activityFilter, timeCol string, next int) (string, []any) {
	if f.After == nil {
		return "", nil
	}
	return fmt.Sprintf(" AND (%s, id) < ($%d, $%d)", timeCol, next, next+1), []any{f.After.OccurredAt, f.After.ID}
}

// authActivity returns up to f.Limit of the tenant's auth_audit_log rows
// with f.UserID as user_id or actor_user_id, newest first. Each branch of
// the UNION is served by its own (tenant_id, user_id|actor_user_id,
// created_at, id) index; UNION drops a row that matches both.
func (s *Store) authActivity(ctx context.Context, tenantID string, f activityFilter) ([]activityJSON, error) {
	after, afterArgs := afterClause(f, "created_at", 4)
	branch := func(col string) string {
		return `(SELECT id, created_at FROM system.auth_audit_log
			WHERE tenant_id = $1 AND ` + col + ` = $2` + after + `
			ORDER BY created_at DESC, id DESC LIMIT $3)`
	}
	query := `
		WITH hits AS (` + branch("user_id") + ` UNION ` + branch("actor_user_id") + `)
		SELECT a.id, a.created_at, a.event_type, a.success, a.failure_reason,
			a.actor_user_id, ap.name, a.user_id, up.name,
			host(a.ip_address), a.user_agent, a.metadata
		FROM hits h
		JOIN system.auth_audit_log a ON a.id = h.id AND a.created_at = h.created_at
		LEFT JOIN system.user_profiles ap ON ap.user_id = a.actor_user_id
		LEFT JOIN system.user_profiles up ON up.user_id = a.user_id
		ORDER BY h.created_at DESC, h.id DESC
		LIMIT $3`
	rows, err := s.db.QueryContext(ctx, query, append([]any{tenantID, f.UserID, f.Limit}, afterArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("query auth activity: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []activityJSON
	for rows.Next() {
		e := activityJSON{Source: sourceAuth}
		var actorID, actorName, userID, userName *string
		var metadata []byte
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.Action, &e.Success, &e.FailureReason,
			&actorID, &actorName, &userID, &userName, &e.IPAddress, &e.UserAgent, &metadata); err != nil {
			return nil, fmt.Errorf("scan auth activity: %w", err)
		}
		e.Actor = person(actorID, actorName)
		e.User = person(userID, userName)
		if metadata != nil {
			e.Metadata = new(jsontext.Value(metadata))
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate auth activity: %w", err)
	}
	return out, nil
}

var recordActions = map[string]string{
	"INSERT": "record.created",
	"UPDATE": "record.updated",
	"DELETE": "record.deleted",
}

// dataActivity returns up to f.Limit of the tenant's audit_log rows
// changed by f.UserID, newest first. changed_fields is computed in the
// query so old_data/new_data values never leave the database: the keys
// present in either snapshot whose values differ.
func (s *Store) dataActivity(ctx context.Context, tenantSlug string, f activityFilter) ([]activityJSON, error) {
	after, afterArgs := afterClause(f, "changed_at", 3)
	query := fmt.Sprintf(`
		SELECT l.id, l.changed_at, l.table_name, l.record_id, l.operation, p.name,
			CASE WHEN l.operation = 'UPDATE' THEN COALESCE((
				SELECT json_agg(k.key ORDER BY k.key)
				FROM (
					SELECT key FROM jsonb_object_keys(COALESCE(l.old_data, '{}')) AS key
					UNION
					SELECT key FROM jsonb_object_keys(COALESCE(l.new_data, '{}')) AS key
				) k
				WHERE l.old_data -> k.key IS DISTINCT FROM l.new_data -> k.key
			), '[]') END
		FROM (
			SELECT id, changed_at, table_name, record_id, operation, old_data, new_data
			FROM %s.audit_log
			WHERE changed_by = $1%s
			ORDER BY changed_at DESC, id DESC
			LIMIT $2
		) l
		LEFT JOIN system.user_profiles p ON p.user_id = $1
		ORDER BY l.changed_at DESC, l.id DESC`, tenantschema.Name(tenantSlug), after)
	rows, err := s.db.QueryContext(ctx, query, append([]any{f.UserID, f.Limit}, afterArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("query data activity: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []activityJSON
	for rows.Next() {
		e := activityJSON{Source: sourceData, Success: true, Record: &recordJSON{}}
		var operation string
		var actorName *string
		var changed []byte
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.table, &e.Record.ID, &operation, &actorName, &changed); err != nil {
			return nil, fmt.Errorf("scan data activity: %w", err)
		}
		e.Action = recordActions[operation]
		e.Actor = &personJSON{ID: f.UserID, Name: actorName}
		if changed != nil {
			var fields []string
			if err := json.Unmarshal(changed, &fields); err != nil {
				return nil, fmt.Errorf("decode changed fields: %w", err)
			}
			e.ChangedFields = &fields
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate data activity: %w", err)
	}
	return out, nil
}

// ServeActivity is GET /admin/users/{id}/activity.
func (h *Handler) ServeActivity(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	id, ok := targetID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	q := r.URL.Query()
	source := q.Get("source")
	if source != "" && source != sourceAuth && source != sourceData {
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_request", "source must be auth or data")
		return
	}
	limit := defaultListLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxListLimit {
			httperr.Write(ctx, w, http.StatusBadRequest, "invalid_request", "limit must be an integer from 1 to 100")
			return
		}
		limit = n
	}
	// One row past limit from each source tells whether another page exists.
	filter := activityFilter{UserID: id, Limit: limit + 1}
	if raw := q.Get("cursor"); raw != "" {
		after, err := decodeActivityCursor(raw)
		if err != nil {
			httperr.Write(ctx, w, http.StatusBadRequest, "invalid_request", "malformed cursor")
			return
		}
		filter.After = after
	}

	if _, err := h.store.get(ctx, c.tenant.Slug, id); err != nil {
		if errors.Is(err, errUserNotFound) {
			httperr.Write(ctx, w, http.StatusNotFound, "not_found", "not found")
			return
		}
		writeInternalError(w, r, err, "target lookup failed")
		return
	}

	var entries []activityJSON
	if source != sourceData {
		rows, err := h.store.authActivity(ctx, c.tenant.TenantID, filter)
		if err != nil {
			writeInternalError(w, r, err, "auth activity failed")
			return
		}
		entries = append(entries, rows...)
	}
	if source != sourceAuth {
		rows, err := h.store.dataActivity(ctx, c.tenant.Slug, filter)
		if err != nil {
			writeInternalError(w, r, err, "data activity failed")
			return
		}
		entries = append(entries, rows...)
	}
	slices.SortFunc(entries, newerFirst)

	var cursor *string
	if len(entries) > limit {
		entries = entries[:limit]
		cursor = new(encodeActivityCursor(entries[limit-1].cursor()))
	}
	for _, e := range entries {
		if e.Record != nil && h.modelForTable != nil {
			if model, ok := h.modelForTable(e.table); ok {
				e.Record.Model = &model
			}
		}
	}
	if entries == nil {
		entries = []activityJSON{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": entries,
		"meta": map[string]any{"cursor": cursor},
	})
}
