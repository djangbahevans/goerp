package adminapi

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/vmihailenco/msgpack/v5"
)

type eventLogFilter struct {
	Tenant string `json:"tenant"`
	Event  string `json:"event"`
	Module string `json:"module"`
	Since  string `json:"since"`
	Until  string `json:"until"`
}

type eventLogCursor struct {
	Filter eventLogFilter `json:"filter"`
	From   time.Time      `json:"from"`
	To     time.Time      `json:"to"`
	At     time.Time      `json:"at"`
	ID     string         `json:"id"`
}

type eventLogItem struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Version   int       `json:"version"`
	Emitter   string    `json:"emitter"`
	Payload   any       `json:"payload"`
	TraceID   *string   `json:"trace_id"`
	UserID    *string   `json:"user_id"`
	EmittedAt time.Time `json:"emitted_at"`
}

type eventLogPage struct {
	Events []eventLogItem `json:"events"`
	Total  int64          `json:"total"`
	Cursor string         `json:"cursor"`
}

func parseEventLogQuery(query url.Values, now time.Time) (eventLogCursor, int, error) {
	var cursor eventLogCursor
	for key, values := range query {
		switch key {
		case "tenant", "event", "module", "since", "until", "limit", "cursor":
		default:
			return cursor, 0, fmt.Errorf("unsupported query parameter %q", key)
		}
		if len(values) != 1 || values[0] == "" {
			return cursor, 0, fmt.Errorf("%s must have one nonempty value", key)
		}
	}

	filter := eventLogFilter{
		Tenant: query.Get("tenant"),
		Event:  query.Get("event"),
		Module: query.Get("module"),
		Since:  cmp.Or(query.Get("since"), "1h"),
		Until:  query.Get("until"),
	}
	if !tenant.ValidSlug(filter.Tenant) {
		return cursor, 0, errors.New("tenant must be a tenant slug")
	}

	limit, err := strconv.Atoi(cmp.Or(query.Get("limit"), "50"))
	if err != nil || limit < 1 || limit > 1000 {
		return cursor, 0, errors.New("limit must be between 1 and 1000")
	}

	if token := query.Get("cursor"); token != "" {
		if len(token) > 8192 {
			return cursor, 0, errors.New("invalid cursor")
		}
		data, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || json.Unmarshal(data, &cursor, json.RejectUnknownMembers(true)) != nil {
			return cursor, 0, errors.New("invalid cursor")
		}
		_, err = uuid.Parse(cursor.ID)
		if err != nil || cursor.Filter != filter || cursor.From.IsZero() || !cursor.From.Before(cursor.To) ||
			cursor.At.Before(cursor.From) || !cursor.At.Before(cursor.To) {
			return cursor, 0, errors.New("cursor does not match the query or contains invalid bounds")
		}
		return cursor, limit, nil
	}

	cursor.Filter = filter
	cursor.To = now.UTC()
	if filter.Until != "" {
		cursor.To, err = time.Parse(time.RFC3339Nano, filter.Until)
		if err != nil {
			return cursor, 0, errors.New("until must be RFC 3339")
		}
	}

	if duration, err := time.ParseDuration(filter.Since); err == nil && duration > 0 {
		cursor.From = cursor.To.Add(-duration)
	} else {
		cursor.From, err = time.Parse(time.RFC3339Nano, filter.Since)
		if err != nil {
			return cursor, 0, errors.New("since must be a positive duration or RFC 3339 timestamp")
		}
	}
	if cursor.From.IsZero() || !cursor.From.Before(cursor.To) {
		return cursor, 0, errors.New("since must precede until")
	}
	return cursor, limit, nil
}

func (h *eventsHandlers) listEvents(w http.ResponseWriter, r *http.Request) {
	cursor, limit, err := parseEventLogQuery(r.URL.Query(), time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	t, err := h.deps.TenantStore.GetBySlug(r.Context(), cursor.Filter.Tenant)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "tenant not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "resolve tenant failed")
		return
	}

	page, err := readEventLog(r.Context(), h.deps.Pool, t.Slug, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "list event log failed")
		return
	}
	writeData(w, http.StatusOK, page)
}

const eventLogWhere = `emitted_at >= $1 AND emitted_at < $2
	AND ($3 = '' OR event_name = $3) AND ($4 = '' OR emitter_module = $4)`

func eventLogSelect(table string, continued bool) string {
	query := `SELECT id, event_name, event_version, emitter_module, payload, trace_id, user_id, emitted_at
		FROM ` + table + ` WHERE ` + eventLogWhere
	if continued {
		query += ` AND (emitted_at, id) < ($6, $7::uuid)`
	}
	return query + ` ORDER BY emitted_at DESC, id DESC LIMIT $5`
}

func readEventLog(ctx context.Context, pool *sql.DB, slug string, cursor eventLogCursor, limit int) (eventLogPage, error) {
	page := eventLogPage{Events: []eventLogItem{}}
	tx, err := pool.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return page, fmt.Errorf("begin event log snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	table := tenantschema.Name(slug) + ".event_log"
	// Round bounds up to preserve >= and < comparisons when PostgreSQL stores
	// microseconds but the request supplies nanoseconds.
	from := cursor.From.Add(time.Microsecond - time.Nanosecond).Truncate(time.Microsecond)
	to := cursor.To.Add(time.Microsecond - time.Nanosecond).Truncate(time.Microsecond)
	args := []any{from, to, cursor.Filter.Event, cursor.Filter.Module}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE `+eventLogWhere, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("count event log: %w", err)
	}
	args = append(args, limit+1)
	if cursor.ID != "" {
		args = append(args, cursor.At, cursor.ID)
	}
	rows, err := tx.QueryContext(ctx, eventLogSelect(table, cursor.ID != ""), args...)
	if err != nil {
		return page, fmt.Errorf("query event log: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var item eventLogItem
		var payload []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Version, &item.Emitter, &payload, &item.TraceID, &item.UserID, &item.EmittedAt); err != nil {
			return page, fmt.Errorf("scan event log: %w", err)
		}
		if len(page.Events) == limit {
			last := page.Events[len(page.Events)-1]
			cursor.At, cursor.ID = last.EmittedAt, last.ID
			data, err := json.Marshal(cursor)
			if err != nil {
				return page, fmt.Errorf("encode event log cursor: %w", err)
			}
			page.Cursor = base64.RawURLEncoding.EncodeToString(data)
			break
		}
		if err := msgpack.Unmarshal(payload, &item.Payload); err != nil {
			return page, fmt.Errorf("decode event log payload: %w", err)
		}
		if _, err := json.Marshal(item.Payload); err != nil {
			return page, fmt.Errorf("encode event log payload: %w", err)
		}
		page.Events = append(page.Events, item)
	}
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("read event log rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return page, fmt.Errorf("close event log rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return page, fmt.Errorf("commit event log snapshot: %w", err)
	}
	return page, nil
}
