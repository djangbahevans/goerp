package adminusers

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

const widgetTable = "widgets"

func modelForTable(table string) (string, bool) {
	if table == widgetTable {
		return "inventory.Widget", true
	}
	return "", false
}

type activityResponse struct {
	Data []activityJSON `json:"data"`
	Meta struct {
		Cursor *string `json:"cursor"`
	} `json:"meta"`
}

// createAuditLog stands in for the engine-owned audit_log table, with its
// columns and its changed_by index.
func (e *env) createAuditLog(t *testing.T, ft fixtureTenant) {
	t.Helper()
	schema := tenantschema.Name(ft.slug)
	if _, err := e.conn.Exec(`CREATE TABLE ` + schema + `.audit_log (
		id UUID NOT NULL DEFAULT uuidv7(),
		table_name TEXT NOT NULL,
		record_id UUID NOT NULL,
		operation TEXT NOT NULL,
		old_data JSONB,
		new_data JSONB,
		changed_by UUID,
		changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		request_id TEXT,
		trace_id TEXT,
		PRIMARY KEY (id, changed_at))`); err != nil {
		t.Fatalf("create audit_log: %v", err)
	}
}

// authRow inserts an auth_audit_log row at the given time and returns its
// id. tenantID "" stores NULL.
func (e *env) authRow(t *testing.T, tenantID, eventType, userID, actorID string, at time.Time) string {
	t.Helper()
	id := uuid.NewV7().String()
	success, failureReason := true, ""
	if eventType == "login.failure" {
		success, failureReason = false, "bad_password"
	}
	if _, err := e.conn.Exec(`
		INSERT INTO system.auth_audit_log (id, event_type, tenant_id, user_id, actor_user_id, ip_address, user_agent, success, failure_reason, metadata, created_at)
		VALUES ($1, $2, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, '203.0.113.9', 'test-agent', $6, NULLIF($7, ''), '{"k": "v"}', $8)`,
		id, eventType, tenantID, userID, actorID, success, failureReason, at); err != nil {
		t.Fatalf("insert auth_audit_log row: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec(`DELETE FROM system.auth_audit_log WHERE id = $1`, id) })
	return id
}

func (e *env) dataRow(t *testing.T, ft fixtureTenant, table, operation, changedBy string, oldData, newData any, at time.Time) string {
	t.Helper()
	marshal := func(v any) []byte {
		if v == nil {
			return nil
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal audit data: %v", err)
		}
		return b
	}
	id := uuid.NewV7().String()
	if _, err := e.conn.Exec(`
		INSERT INTO `+tenantschema.Name(ft.slug)+`.audit_log (id, table_name, record_id, operation, old_data, new_data, changed_by, changed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, table, uuid.New().String(), operation, marshal(oldData), marshal(newData), changedBy, at); err != nil {
		t.Fatalf("insert audit_log row: %v", err)
	}
	return id
}

func (e *env) activity(t *testing.T, ft fixtureTenant, token, userID, query string) (activityResponse, string) {
	t.Helper()
	rec := do(t, ft, token, request{
		serve: e.handler.ServeActivity, method: http.MethodGet,
		path: "/admin/users/" + userID + "/activity" + query, params: map[string]string{"id": userID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET activity%s status = %d, body = %s", query, rec.Code, rec.Body)
	}
	var out activityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode activity: %v", err)
	}
	return out, rec.Body.String()
}

func activityIDs(entries []activityJSON) []string {
	out := make([]string, len(entries))
	for i, a := range entries {
		out[i] = a.ID
	}
	return out
}

func TestServeActivity_OnlyTheCallingTenantsRows(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	other := e.newTenant(t)
	e.createAuditLog(t, ft)
	e.createAuditLog(t, other)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	e.grant(t, other, target, "user")
	now := time.Now()

	want := []string{
		e.authRow(t, ft.id, "user.suspended", target, admin, now.Add(-1*time.Second)),
		e.authRow(t, ft.id, "role.created", "", target, now.Add(-2*time.Second)),
		e.dataRow(t, ft, widgetTable, "INSERT", target, nil, map[string]any{"name": "w"}, now.Add(-3*time.Second)),
	}
	e.authRow(t, other.id, "user.suspended", target, admin, now)
	e.authRow(t, "", "login.failure", target, "", now)
	e.dataRow(t, other, widgetTable, "INSERT", target, nil, map[string]any{"name": "w"}, now)
	e.dataRow(t, ft, widgetTable, "INSERT", admin, nil, map[string]any{"name": "w"}, now)

	got, _ := e.activity(t, ft, e.issue(t, ft, admin), target, "")
	if ids := activityIDs(got.Data); !slices.Equal(ids, want) {
		t.Fatalf("activity ids = %v, want %v", ids, want)
	}
	if got.Meta.Cursor != nil {
		t.Errorf("cursor = %q on the only page, want null", *got.Meta.Cursor)
	}

	suspended := got.Data[0]
	if suspended.Source != "auth" || suspended.Action != "user.suspended" || !suspended.Success {
		t.Errorf("auth entry = %+v", suspended)
	}
	if suspended.Actor == nil || suspended.Actor.ID != admin || suspended.Actor.Name == nil || *suspended.Actor.Name != "Admin" {
		t.Errorf("auth actor = %+v, want the admin", suspended.Actor)
	}
	if suspended.User == nil || suspended.User.ID != target || suspended.User.Name == nil || *suspended.User.Name != "Target" {
		t.Errorf("auth user = %+v, want the target", suspended.User)
	}
	if suspended.IPAddress == nil || *suspended.IPAddress != "203.0.113.9" || suspended.UserAgent == nil || *suspended.UserAgent != "test-agent" {
		t.Errorf("auth ip/user agent = %v/%v", suspended.IPAddress, suspended.UserAgent)
	}
	if suspended.Metadata == nil || string(*suspended.Metadata) != `{"k":"v"}` {
		t.Errorf("auth metadata = %v, want {\"k\":\"v\"}", suspended.Metadata)
	}
	if suspended.Record != nil || suspended.ChangedFields != nil {
		t.Errorf("auth entry has record %+v / changed_fields %v, want null", suspended.Record, suspended.ChangedFields)
	}
	if got.Data[1].User != nil {
		t.Errorf("role.created user = %+v, want null", got.Data[1].User)
	}

	created := got.Data[2]
	if created.Source != "data" || created.Action != "record.created" || !created.Success || created.User != nil {
		t.Errorf("data entry = %+v", created)
	}
	if created.Record == nil || created.Record.Model == nil || *created.Record.Model != "inventory.Widget" {
		t.Errorf("data record = %+v, want model inventory.Widget", created.Record)
	}
	if created.ChangedFields != nil || created.Metadata != nil || created.IPAddress != nil {
		t.Errorf("record.created carries auth-only or update-only fields: %+v", created)
	}
}

func TestServeActivity_SourceFilter(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.createAuditLog(t, ft)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	now := time.Now()
	authID := e.authRow(t, ft.id, "password.changed", target, target, now)
	dataID := e.dataRow(t, ft, widgetTable, "DELETE", target, map[string]any{"name": "w"}, nil, now.Add(-time.Second))
	token := e.issue(t, ft, admin)

	for query, want := range map[string][]string{
		"":             {authID, dataID},
		"?source=auth": {authID},
		"?source=data": {dataID},
	} {
		got, _ := e.activity(t, ft, token, target, query)
		if ids := activityIDs(got.Data); !slices.Equal(ids, want) {
			t.Errorf("activity%s ids = %v, want %v", query, ids, want)
		}
	}

	for _, query := range []string{"?source=other", "?limit=0", "?limit=101", "?cursor=%21%21", "?cursor=" + encodeActivityCursor(activityCursor{OccurredAt: now, ID: "x"})} {
		rec := do(t, ft, token, request{
			serve: e.handler.ServeActivity, method: http.MethodGet,
			path: "/admin/users/" + target + "/activity" + query, params: map[string]string{"id": target},
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("activity%s status = %d, want 400", query, rec.Code)
		}
	}
}

func TestServeActivity_PagesAcrossBothSources(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.createAuditLog(t, ft)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	base := time.Now().Truncate(time.Second)

	// Several entries share a timestamp across sources, so the id
	// tie-break decides their order and the cursor must respect it.
	var all []activityJSON
	for i := range 7 {
		at := base.Add(-time.Duration(i/2) * time.Second)
		var id string
		if i%3 == 0 {
			id = e.dataRow(t, ft, widgetTable, "INSERT", target, nil, map[string]any{"n": i}, at)
		} else {
			id = e.authRow(t, ft.id, "login.success", target, target, at)
		}
		all = append(all, activityJSON{ID: id, OccurredAt: at})
	}
	slices.SortFunc(all, newerFirst)
	want := activityIDs(all)
	token := e.issue(t, ft, admin)

	var got []string
	query := "?limit=3"
	for page := 0; ; page++ {
		if page > len(want) {
			t.Fatalf("paging did not terminate; ids so far %v", got)
		}
		resp, _ := e.activity(t, ft, token, target, query)
		got = append(got, activityIDs(resp.Data)...)
		if resp.Meta.Cursor == nil {
			break
		}
		if len(resp.Data) != 3 {
			t.Errorf("page %d has %d entries and a cursor, want 3", page, len(resp.Data))
		}
		query = "?limit=3&cursor=" + *resp.Meta.Cursor
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paged ids = %v, want %v", got, want)
	}
}

func TestServeActivity_ChangedFieldsWithoutValues(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.createAuditLog(t, ft)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	now := time.Now()

	oldData := map[string]any{"name": "old-secret-name", "qty": 1, "note": "kept-secret-note", "gone": "removed-secret"}
	newData := map[string]any{"name": "new-secret-name", "qty": 1, "note": "kept-secret-note", "added": "added-secret"}
	e.dataRow(t, ft, widgetTable, "UPDATE", target, oldData, newData, now)
	e.dataRow(t, ft, "dropped_table", "UPDATE", target, map[string]any{"a": 1}, map[string]any{"a": 1}, now.Add(-time.Second))

	got, body := e.activity(t, ft, e.issue(t, ft, admin), target, "")
	if len(got.Data) != 2 {
		t.Fatalf("got %d entries, want 2: %s", len(got.Data), body)
	}
	updated := got.Data[0]
	if updated.Action != "record.updated" {
		t.Errorf("action = %q, want record.updated", updated.Action)
	}
	if want := []string{"added", "gone", "name"}; updated.ChangedFields == nil || !slices.Equal(*updated.ChangedFields, want) {
		t.Errorf("changed_fields = %v, want %v", updated.ChangedFields, want)
	}
	unchanged := got.Data[1]
	if unchanged.ChangedFields == nil || len(*unchanged.ChangedFields) != 0 {
		t.Errorf("changed_fields for an update with no differences = %v, want []", unchanged.ChangedFields)
	}
	if unchanged.Record == nil || unchanged.Record.Model != nil {
		t.Errorf("record for a table no model owns = %+v, want model null", unchanged.Record)
	}
	for _, v := range []string{"old-secret-name", "new-secret-name", "kept-secret-note", "removed-secret", "added-secret"} {
		if strings.Contains(body, v) {
			t.Errorf("response contains the audited value %q: %s", v, body)
		}
	}
}

func TestServeActivity_InviteeAndUnknownUser(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.createAuditLog(t, ft)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	token := e.issue(t, ft, admin)
	email := fmt.Sprintf("invitee%d@example.com", time.Now().UnixNano())
	if _, err := e.invites.Invite(t.Context(), ft.slug, email, "user", "Invitee", nil); err != nil {
		t.Fatalf("Invite() error: %v", err)
	}
	invitee, err := e.users.GetByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("GetByEmail(invitee) error: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec(`DELETE FROM system.users WHERE id = $1`, invitee.ID) })

	got, _ := e.activity(t, ft, token, invitee.ID, "")
	if got.Data == nil {
		t.Error("invitee activity data = null, want []")
	}

	unknown := uuid.New().String()
	rec := do(t, ft, token, request{
		serve: e.handler.ServeActivity, method: http.MethodGet,
		path: "/admin/users/" + unknown + "/activity", params: map[string]string{"id": unknown},
	})
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown user status = %d, want 404", rec.Code)
	}
}
