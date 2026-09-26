package engine

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/scheduledactivity"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	sdkmodel "github.com/djangbahevans/goerp/sdk/go/model"
)

// scheduledActivityFixture is dispatchActivityFixture plus a bootstrapped
// scheduled_activities table and tenant roles, so assignees can be real
// tenant members. memberID is an active member; the caller needs no
// membership of their own, since auth middleware has already vetted them.
type scheduledActivityFixture struct {
	*dispatchActivityFixture
	// admin is the superuser connection, kept for fixture writes after
	// restrictWidgetsToOwner switches e.primaryDB to a restricted role.
	admin      *sql.DB
	userRoleID string
	memberID   string
}

func newScheduledActivityFixture(t *testing.T) *scheduledActivityFixture {
	t.Helper()
	af := newDispatchActivityFixture(t)
	ctx := t.Context()

	store := scheduledactivity.NewStore(af.e.primaryDB)
	if err := store.Bootstrap(ctx, af.slug); err != nil {
		t.Fatalf("scheduledactivity Bootstrap() error: %v", err)
	}
	roles := role.NewStore(af.e.primaryDB)
	if err := roles.Bootstrap(ctx, af.slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := roles.SeedBuiltinRoles(ctx, af.slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	userRoleID, err := roles.GetRoleByName(ctx, af.slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	af.e.scheduledActivityStore = store
	af.e.roleStore = roles
	af.e.rolePermissionMap = permcache.NewRolePermissionMap()

	f := &scheduledActivityFixture{dispatchActivityFixture: af, admin: af.e.primaryDB, userRoleID: userRoleID}
	f.memberID = f.newUser(t, user.StatusActive, true)
	return f
}

// newUser creates a user with status, holding the tenant's "user" role
// when member is true.
func (f *scheduledActivityFixture) newUser(t *testing.T, status user.Status, member bool) string {
	t.Helper()
	email := fmt.Sprintf("scheduled%d@example.com", time.Now().UnixNano())
	id, err := f.e.userStore.CreateRegistered(t.Context(), email, "", status)
	if err != nil {
		t.Fatalf("CreateRegistered() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.admin.Exec(`DELETE FROM system.users WHERE id = $1`, id) })
	if member {
		if err := f.e.roleStore.AssignRole(t.Context(), f.slug, id, f.userRoleID, ""); err != nil {
			t.Fatalf("AssignRole() error: %v", err)
		}
	}
	return id
}

func (f *scheduledActivityFixture) do(t *testing.T, callerID, method, target string, body any, handler func(http.ResponseWriter, *http.Request), pathParams map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	w := httptest.NewRecorder()
	handler(w, f.requestAs(callerID, method, target, raw, pathParams))
	return w
}

func (f *scheduledActivityFixture) create(t *testing.T, callerID string, fields map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"model": activityTestModel, "record_id": f.recordID, "type": "call", "summary": "Confirm Friday delivery", "due_date": "2026-09-25"}
	maps.Copy(body, fields)
	return f.do(t, callerID, http.MethodPost, "/_meta/scheduled-activities", body, f.e.dispatchScheduledActivityCreateRoute, nil)
}

func (f *scheduledActivityFixture) schedule(t *testing.T, callerID string, fields map[string]any) map[string]any {
	t.Helper()
	w := f.create(t, callerID, fields)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	return decodeObject(t, w)
}

func (f *scheduledActivityFixture) patch(t *testing.T, callerID, id string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, callerID, http.MethodPatch, "/_meta/scheduled-activities/"+id, body, f.e.dispatchScheduledActivityUpdateRoute, map[string]string{"id": id})
}

func (f *scheduledActivityFixture) done(t *testing.T, callerID, id string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, callerID, http.MethodPost, "/_meta/scheduled-activities/"+id+"/done", body, f.e.dispatchScheduledActivityDoneRoute, map[string]string{"id": id})
}

func (f *scheduledActivityFixture) cancel(t *testing.T, callerID, id string) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, callerID, http.MethodDelete, "/_meta/scheduled-activities/"+id, nil, f.e.dispatchScheduledActivityCancelRoute, map[string]string{"id": id})
}

func (f *scheduledActivityFixture) listForRecord(t *testing.T, recordID string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"model": {activityTestModel}, "record_id": {recordID}}
	return f.do(t, f.callerID, http.MethodGet, "/_meta/scheduled-activities?"+q.Encode(), nil, f.e.dispatchScheduledActivityListRoute, nil)
}

func (f *scheduledActivityFixture) mine(t *testing.T, callerID string, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, callerID, http.MethodGet, "/_meta/scheduled-activities/mine?"+query.Encode(), nil, f.e.dispatchScheduledActivityMineRoute, nil)
}

// insertWidget adds a widget row, optionally visible only to one user
// under restrictWidgetsToOwner's policy.
func (f *scheduledActivityFixture) insertWidget(t *testing.T, name string, ownerID *string) string {
	t.Helper()
	var id string
	if err := f.admin.QueryRowContext(t.Context(), fmt.Sprintf(
		`INSERT INTO %s.widget (tenant_id, name, internal_ref) VALUES ($1, $2, $3) RETURNING id`, tenantschema.Name(f.slug),
	), f.tenantID, name, ownerID).Scan(&id); err != nil {
		t.Fatalf("insert widget: %v", err)
	}
	return id
}

func (f *scheduledActivityFixture) deleteWidget(t *testing.T, id string) {
	t.Helper()
	if _, err := f.admin.ExecContext(t.Context(), fmt.Sprintf(`DELETE FROM %s.widget WHERE id = $1`, tenantschema.Name(f.slug)), id); err != nil {
		t.Fatalf("delete widget: %v", err)
	}
}

// restrictWidgetsToOwner makes host.orm.read enforce a row-level policy: a
// widget with an internal_ref is readable only by the user it names. The
// dev stack's goerp role is a superuser that RLS never applies to, so
// record reads are switched to a restricted login role. Stores keep their
// own superuser connections.
func (f *scheduledActivityFixture) restrictWidgetsToOwner(t *testing.T) {
	t.Helper()
	admin := f.admin
	schema := tenantschema.Name(f.slug)
	const roleName = "goerp_test_rls_reader_engine"
	stmts := []string{
		`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + roleName + `') THEN
				CREATE ROLE ` + roleName + ` LOGIN PASSWORD 'dev' NOSUPERUSER NOBYPASSRLS;
			END IF;
		END $$`,
		"GRANT USAGE ON SCHEMA " + schema + " TO " + roleName,
		"GRANT SELECT ON " + schema + ".widget TO " + roleName,
		"ALTER TABLE " + schema + ".widget ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY widget_owner ON " + schema + ".widget FOR SELECT USING (internal_ref IS NULL OR internal_ref = current_setting('app.current_user_id', true))",
	}
	for _, stmt := range stmts {
		if _, err := admin.ExecContext(t.Context(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	reader, err := db.New("postgres://" + roleName + ":dev@localhost:55432/goerp")
	if err != nil {
		t.Fatalf("connect as %s: %v", roleName, err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	f.e.primaryDB = reader
	t.Cleanup(func() { f.e.primaryDB = admin })
}

func decodeObject(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, w.Body.String())
	}
	return obj
}

func wantError(t *testing.T, w *httptest.ResponseRecorder, status int, code, what string) {
	t.Helper()
	if w.Code != status || decodeErrorCode(t, w) != code {
		t.Errorf("%s: status = %d, want %d %s; body: %s", what, w.Code, status, code, w.Body.String())
	}
}

func TestDispatchScheduledActivity_CreateDefaultsTheAssigneeToTheCaller(t *testing.T) {
	f := newScheduledActivityFixture(t)

	a := f.schedule(t, f.callerID, map[string]any{"note": "  They asked for a morning slot. "})

	assignee := a["assignee"].(map[string]any)
	creator := a["created_by"].(map[string]any)
	if assignee["id"] != f.callerID || creator["id"] != f.callerID || creator["name"] != "Ama Owusu" {
		t.Errorf("assignee = %v, created_by = %v; want both the caller, with their profile name", assignee, creator)
	}
	if a["model"] != activityTestModel || a["record_id"] != f.recordID || a["type"] != "call" || a["due_date"] != "2026-09-25" {
		t.Errorf("activity = %v, want the requested model, record, type and due date", a)
	}
	if a["note"] != "They asked for a morning slot." || a["summary"] != "Confirm Friday delivery" {
		t.Errorf("note = %q, summary = %q; want trimmed text", a["note"], a["summary"])
	}
	for _, k := range []string{"done_at", "done_by", "feedback"} {
		if v, ok := a[k]; !ok || v != nil {
			t.Errorf("%s = %v (present %v), want an explicit null", k, v, ok)
		}
	}
}

func TestDispatchScheduledActivity_ListReturnsTheRecordsOpenActivitiesSoonestFirst(t *testing.T) {
	f := newScheduledActivityFixture(t)
	later := f.schedule(t, f.callerID, map[string]any{"due_date": "2026-10-02"})
	sooner := f.schedule(t, f.callerID, map[string]any{"due_date": "2026-09-24"})
	completed := f.schedule(t, f.callerID, map[string]any{"due_date": "2026-09-20"})
	if w := f.done(t, f.callerID, completed["id"].(string), nil); w.Code != http.StatusOK {
		t.Fatalf("done status = %d; body: %s", w.Code, w.Body.String())
	}

	w := f.listForRecord(t, f.recordID)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d; body: %s", w.Code, w.Body.String())
	}
	data := decodeObject(t, w)["data"].([]any)
	if len(data) != 2 || data[0].(map[string]any)["id"] != sooner["id"] || data[1].(map[string]any)["id"] != later["id"] {
		t.Errorf("data = %v, want [%v, %v]", data, sooner["id"], later["id"])
	}
}

func TestDispatchScheduledActivity_DeniesACallerWhoCannotReadTheRecord(t *testing.T) {
	f := newScheduledActivityFixture(t)
	missing := "99999999-9999-9999-9999-999999999999"

	wantError(t, f.listForRecord(t, missing), http.StatusForbidden, "permission_denied", "GET")
	wantError(t, f.create(t, f.callerID, map[string]any{"record_id": missing}), http.StatusForbidden, "permission_denied", "POST")

	// An activity whose record is gone answers exactly like one on a
	// record the caller can't read.
	gone := f.insertWidget(t, "Gone", nil)
	a := f.schedule(t, f.callerID, map[string]any{"record_id": gone})
	f.deleteWidget(t, gone)
	id := a["id"].(string)
	wantError(t, f.patch(t, f.callerID, id, map[string]any{"summary": "x"}), http.StatusForbidden, "permission_denied", "PATCH")
	wantError(t, f.done(t, f.callerID, id, nil), http.StatusForbidden, "permission_denied", "done")
	wantError(t, f.cancel(t, f.callerID, id), http.StatusForbidden, "permission_denied", "DELETE")

	unknown := "99999999-9999-9999-9999-999999999999"
	wantError(t, f.patch(t, f.callerID, unknown, map[string]any{"summary": "x"}), http.StatusNotFound, "not_found", "PATCH unknown")
	wantError(t, f.done(t, f.callerID, unknown, nil), http.StatusNotFound, "not_found", "done unknown")
	wantError(t, f.cancel(t, f.callerID, unknown), http.StatusNotFound, "not_found", "DELETE unknown")
}

func TestDispatchScheduledActivity_AssigneeMustBeAnActiveMember(t *testing.T) {
	f := newScheduledActivityFixture(t)
	cases := map[string]string{
		"no role in the tenant": f.newUser(t, user.StatusActive, false),
		"suspended member":      f.newUser(t, user.StatusSuspended, true),
		"invited member":        f.newUser(t, user.StatusInvited, true),
		"no such user":          "99999999-9999-9999-9999-999999999999",
	}
	for name, assignee := range cases {
		wantError(t, f.create(t, f.callerID, map[string]any{"assignee_id": assignee}), http.StatusBadRequest, "invalid_assignee", name)
	}

	a := f.schedule(t, f.callerID, map[string]any{"assignee_id": f.memberID})
	if a["assignee"].(map[string]any)["id"] != f.memberID {
		t.Errorf("assignee = %v, want the member", a["assignee"])
	}
	for name, assignee := range cases {
		wantError(t, f.patch(t, f.callerID, a["id"].(string), map[string]any{"assignee_id": assignee}), http.StatusBadRequest, "invalid_assignee", "PATCH "+name)
	}
}

func TestDispatchScheduledActivity_AssigneeMustBeAbleToReadTheRecord(t *testing.T) {
	f := newScheduledActivityFixture(t)
	f.restrictWidgetsToOwner(t)
	private := f.insertWidget(t, "Caller's own", &f.callerID)

	wantError(t, f.create(t, f.callerID, map[string]any{"record_id": private, "assignee_id": f.memberID}), http.StatusBadRequest, "invalid_assignee", "assign private record to member")
	wantError(t, f.create(t, f.memberID, map[string]any{"record_id": private}), http.StatusForbidden, "permission_denied", "member schedules on private record")

	a := f.schedule(t, f.callerID, map[string]any{"record_id": private})
	wantError(t, f.patch(t, f.callerID, a["id"].(string), map[string]any{"assignee_id": f.memberID}), http.StatusBadRequest, "invalid_assignee", "reassign private record to member")

	shared := f.schedule(t, f.callerID, map[string]any{"assignee_id": f.memberID})
	if shared["assignee"].(map[string]any)["id"] != f.memberID {
		t.Errorf("assignee = %v, want the member, who can read the public widget", shared["assignee"])
	}
}

func TestDispatchScheduledActivity_OnlyTheCreatorOrAssigneeCanChangeIt(t *testing.T) {
	f := newScheduledActivityFixture(t)
	outsider := f.newUser(t, user.StatusActive, true)
	a := f.schedule(t, f.callerID, map[string]any{"assignee_id": f.memberID})
	id := a["id"].(string)

	wantError(t, f.patch(t, outsider, id, map[string]any{"summary": "x"}), http.StatusForbidden, "not_participant", "PATCH")
	wantError(t, f.done(t, outsider, id, nil), http.StatusForbidden, "not_participant", "done")
	wantError(t, f.cancel(t, outsider, id), http.StatusForbidden, "not_participant", "DELETE")

	if w := f.patch(t, f.memberID, id, map[string]any{"summary": "Reconfirm"}); w.Code != http.StatusOK {
		t.Errorf("assignee PATCH status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if w := f.patch(t, f.callerID, id, map[string]any{"due_date": "2026-10-01"}); w.Code != http.StatusOK {
		t.Errorf("creator PATCH status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchScheduledActivity_PatchEditsFieldsAndClearsTheNote(t *testing.T) {
	f := newScheduledActivityFixture(t)
	a := f.schedule(t, f.callerID, map[string]any{"note": "Morning slot"})
	id := a["id"].(string)

	w := f.patch(t, f.callerID, id, map[string]any{"type": "meeting", "due_date": "2026-10-01", "assignee_id": f.memberID})
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; body: %s", w.Code, w.Body.String())
	}
	got := decodeObject(t, w)
	if got["type"] != "meeting" || got["due_date"] != "2026-10-01" || got["note"] != "Morning slot" || got["summary"] != "Confirm Friday delivery" {
		t.Errorf("activity = %v, want the new type and date, other fields unchanged", got)
	}
	if got["assignee"].(map[string]any)["id"] != f.memberID {
		t.Errorf("assignee = %v, want the member", got["assignee"])
	}

	w = f.patch(t, f.callerID, id, map[string]any{"note": nil})
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH note null status = %d; body: %s", w.Code, w.Body.String())
	}
	if got := decodeObject(t, w); got["note"] != nil {
		t.Errorf("note = %v, want null after clearing", got["note"])
	}
}

func TestDispatchScheduledActivity_DoneRecordsOneFeedEntryAndIsFinal(t *testing.T) {
	f := newScheduledActivityFixture(t)
	a := f.schedule(t, f.callerID, map[string]any{"assignee_id": f.memberID})
	id := a["id"].(string)

	w := f.done(t, f.memberID, id, map[string]any{"feedback": "Customer confirmed Friday delivery."})
	if w.Code != http.StatusOK {
		t.Fatalf("done status = %d; body: %s", w.Code, w.Body.String())
	}
	got := decodeObject(t, w)
	if got["done_at"] == nil || got["done_by"].(map[string]any)["id"] != f.memberID || got["feedback"] != "Customer confirmed Friday delivery." {
		t.Errorf("activity = %v, want done by the member with the feedback", got)
	}

	wantError(t, f.patch(t, f.callerID, id, map[string]any{"summary": "x"}), http.StatusConflict, "activity_done", "PATCH")
	wantError(t, f.done(t, f.callerID, id, nil), http.StatusConflict, "activity_done", "done again")
	wantError(t, f.cancel(t, f.callerID, id), http.StatusConflict, "activity_done", "DELETE")

	feed := f.listPage(t, "", 20)
	if len(feed.Data) != 1 {
		t.Fatalf("feed = %v, want exactly one entry", feed.Data)
	}
	entry := feed.Data[0]
	if entry["kind"] != "activity_done" || entry["author"].(map[string]any)["id"] != f.memberID {
		t.Errorf("entry = %v, want an activity_done by the member", entry)
	}
	snap := entry["activity"].(map[string]any)
	want := map[string]any{"activity_id": id, "type": "call", "summary": "Confirm Friday delivery", "due_date": "2026-09-25", "feedback": "Customer confirmed Friday delivery."}
	for k, v := range want {
		if snap[k] != v {
			t.Errorf("activity[%s] = %v, want %v", k, snap[k], v)
		}
	}
}

func TestDispatchScheduledActivity_DoneWithoutABodyHasNoFeedback(t *testing.T) {
	f := newScheduledActivityFixture(t)
	a := f.schedule(t, f.callerID, nil)

	w := f.done(t, f.callerID, a["id"].(string), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("done status = %d; body: %s", w.Code, w.Body.String())
	}
	if got := decodeObject(t, w); got["feedback"] != nil {
		t.Errorf("feedback = %v, want null", got["feedback"])
	}
}

func TestDispatchScheduledActivity_CancelLeavesNoRowAndNoFeedEntry(t *testing.T) {
	f := newScheduledActivityFixture(t)
	a := f.schedule(t, f.callerID, nil)

	if w := f.cancel(t, f.callerID, a["id"].(string)); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if data := decodeObject(t, f.listForRecord(t, f.recordID))["data"].([]any); len(data) != 0 {
		t.Errorf("open activities = %v, want none", data)
	}
	var n int
	if err := f.e.primaryDB.QueryRowContext(t.Context(), fmt.Sprintf(`SELECT count(*) FROM %s.scheduled_activities`, tenantschema.Name(f.slug))).Scan(&n); err != nil {
		t.Fatalf("count activities: %v", err)
	}
	if n != 0 {
		t.Errorf("%d rows left, want 0", n)
	}
	if feed := f.listPage(t, "", 20); len(feed.Data) != 0 {
		t.Errorf("feed = %v, want empty", feed.Data)
	}
}

func TestDispatchScheduledActivity_MinePagesSoonestFirstAndOmitsUnreadableRecords(t *testing.T) {
	f := newScheduledActivityFixture(t)
	gone := f.insertWidget(t, "Gone", nil)
	var want []string
	for _, d := range []string{"2026-09-27", "2026-09-25", "2026-09-25", "2026-09-26", "2026-09-24"} {
		want = append(want, f.schedule(t, f.callerID, map[string]any{"due_date": d})["id"].(string))
	}
	f.schedule(t, f.callerID, map[string]any{"record_id": gone, "due_date": "2026-09-20"})
	f.schedule(t, f.callerID, map[string]any{"assignee_id": f.memberID})
	f.deleteWidget(t, gone)

	type item struct{ id, due string }
	var got []item
	cursor := ""
	for page := 0; ; page++ {
		if page > 10 {
			t.Fatal("pagination did not terminate")
		}
		q := url.Values{"limit": {"2"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		w := f.mine(t, f.callerID, q)
		if w.Code != http.StatusOK {
			t.Fatalf("GET mine status = %d; body: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Data []map[string]any `json:"data"`
			Meta activityListMeta `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, a := range resp.Data {
			if a["record_name"] != "Widget A" {
				t.Errorf("record_name = %v, want Widget A", a["record_name"])
			}
			got = append(got, item{a["id"].(string), a["due_date"].(string)})
		}
		if !resp.Meta.HasMore {
			if resp.Meta.Cursor != nil {
				t.Errorf("cursor = %v on the last page, want null", *resp.Meta.Cursor)
			}
			break
		}
		cursor = *resp.Meta.Cursor
	}

	if len(got) != len(want) {
		t.Fatalf("got %d activities %v, want the %d on readable records assigned to the caller", len(got), got, len(want))
	}
	seen := map[string]bool{}
	for i, a := range got {
		if seen[a.id] {
			t.Errorf("activity %s returned twice", a.id)
		}
		seen[a.id] = true
		if i > 0 && (a.due < got[i-1].due || a.due == got[i-1].due && a.id < got[i-1].id) {
			t.Errorf("activity %d %v sorts before %v", i, a, got[i-1])
		}
	}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("activity %s missing", id)
		}
	}
}

func TestDispatchScheduledActivity_RejectsInvalidRequests(t *testing.T) {
	f := newScheduledActivityFixture(t)

	creates := map[string]map[string]any{
		"empty summary":     {"summary": "   "},
		"long summary":      {"summary": strings.Repeat("x", 201)},
		"long note":         {"note": strings.Repeat("x", 10001)},
		"bad due date":      {"due_date": "25/09/2026"},
		"missing due date":  {"due_date": ""},
		"bad assignee":      {"assignee_id": "someone"},
		"bad record_id":     {"record_id": "nope"},
		"missing record_id": {"record_id": ""},
	}
	for name, fields := range creates {
		wantError(t, f.create(t, f.callerID, fields), http.StatusBadRequest, "invalid_request", name)
	}
	wantError(t, f.create(t, f.callerID, map[string]any{"type": "visit"}), http.StatusBadRequest, "invalid_type", "unknown type")
	wantError(t, f.create(t, f.callerID, map[string]any{"model": "testmodule.nope"}), http.StatusBadRequest, "model_not_found", "unknown model")

	id := f.schedule(t, f.callerID, nil)["id"].(string)
	patches := map[string]map[string]any{
		"empty summary": {"summary": ""},
		"long note":     {"note": strings.Repeat("x", 10001)},
		"note number":   {"note": 5},
		"bad due date":  {"due_date": "tomorrow"},
		"bad assignee":  {"assignee_id": "someone"},
	}
	for name, body := range patches {
		wantError(t, f.patch(t, f.callerID, id, body), http.StatusBadRequest, "invalid_request", "PATCH "+name)
	}
	wantError(t, f.patch(t, f.callerID, id, map[string]any{"type": "visit"}), http.StatusBadRequest, "invalid_type", "PATCH unknown type")
	wantError(t, f.done(t, f.callerID, id, map[string]any{"feedback": strings.Repeat("x", 10001)}), http.StatusBadRequest, "invalid_request", "long feedback")

	for name, q := range map[string]url.Values{
		"malformed cursor": {"cursor": {"not-a-cursor"}},
		"cursor bad date":  {"cursor": {encodeScheduledActivityCursor("2026-13-01", id)}},
		"limit zero":       {"limit": {"0"}},
		"limit too big":    {"limit": {"201"}},
	} {
		wantError(t, f.mine(t, f.callerID, q), http.StatusBadRequest, "invalid_request", name)
	}
}

func TestRecordLabelFields_FollowsTheLabelFieldChain(t *testing.T) {
	const name = "testmodule.order"
	decl := func(primary string, fields ...string) sdkmodel.ModelDeclaration {
		md := sdkmodel.ModelDeclaration{Fields: []sdkmodel.NamedField{{Name: "id", Def: sdkmodel.UUID().PrimaryKey()}}}
		for _, f := range fields {
			def := sdkmodel.Text()
			if f == primary {
				def = def.Primary()
			}
			md.Fields = append(md.Fields, sdkmodel.NamedField{Name: f, Def: def})
		}
		return md
	}
	listView := func(labelField string, columns ...manifest.ListColumn) []manifest.View {
		return []manifest.View{
			{Type: "form", Resource: name, LabelField: "code"},
			{Type: "list", Resource: "testmodule.other", LabelField: "code"},
			{Type: "list", Resource: name, LabelField: labelField, Columns: columns},
		}
	}

	cases := []struct {
		name  string
		md    sdkmodel.ModelDeclaration
		views []manifest.View
		want  string
	}{
		{"view label_field", decl("ref", "code", "ref", "name"), listView("code", manifest.ListColumn{Field: "ref", Primary: true}), "code"},
		{"primary column", decl("ref", "code", "ref", "name"), listView("", manifest.ListColumn{Field: "name"}, manifest.ListColumn{Field: "code", Primary: true}), "code"},
		{"undeclared view field skipped", decl("ref", "ref"), listView("missing", manifest.ListColumn{Field: "gone", Primary: true}), "ref"},
		{".Primary() field", decl("ref", "ref", "name"), nil, "ref"},
		{"display_name before name", decl("", "name", "display_name"), nil, "display_name"},
		{"title", decl("", "title"), nil, "title"},
		{"primary key", decl(""), nil, "id"},
	}
	for _, c := range cases {
		pk, label := recordLabelFields(c.md, name, c.views)
		if pk != "id" || label != c.want {
			t.Errorf("%s: got (%s, %s), want (id, %s)", c.name, pk, label, c.want)
		}
	}
}
