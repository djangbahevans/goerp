package engine

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

const activityTestModel = "testmodule.widget"

// dispatchActivityFixture is dispatchSharesFixture's Engine and widget
// record plus a bootstrapped record_activity table. The caller is the
// fixture's real recipient user, given a profile so author hydration has
// a name to return; otherCallerID is a second user with no profile.
type dispatchActivityFixture struct {
	*dispatchSharesFixture
	callerID      string
	otherCallerID string
}

func newDispatchActivityFixture(t *testing.T) *dispatchActivityFixture {
	t.Helper()
	sf := newDispatchSharesFixture(t)

	store := recordactivity.NewStore(sf.e.primaryDB)
	if err := store.Bootstrap(t.Context(), sf.slug); err != nil {
		t.Fatalf("recordactivity Bootstrap() error: %v", err)
	}
	sf.e.recordActivityStore = store

	if err := sf.e.userStore.EnsureProfile(t.Context(), sf.recipientID, "Ama Owusu"); err != nil {
		t.Fatalf("EnsureProfile() error: %v", err)
	}

	return &dispatchActivityFixture{
		dispatchSharesFixture: sf,
		callerID:              sf.recipientID,
		otherCallerID:         sf.sharerID,
	}
}

func (f *dispatchActivityFixture) requestAs(callerID, method, target string, body []byte, pathParams map[string]string) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	ctx := withTenantContext(r.Context(), &tenantresolve.TenantContext{TenantID: f.tenantID, Slug: f.slug})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: callerID})
	if pathParams != nil {
		ctx = route.WithParams(ctx, pathParams)
	}
	return r.WithContext(ctx)
}

func (f *dispatchActivityFixture) post(t *testing.T, callerID string, fields map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(fields)
	w := httptest.NewRecorder()
	f.e.dispatchActivityCreateRoute(w, f.requestAs(callerID, http.MethodPost, "/_meta/activity", body, nil))
	return w
}

func (f *dispatchActivityFixture) postComment(t *testing.T, callerID, text string) activityEntryResponse {
	t.Helper()
	w := f.post(t, callerID, map[string]any{"model": activityTestModel, "record_id": f.recordID, "body": text})
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var resp activityEntryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode POST response: %v", err)
	}
	return resp
}

type activityListResponse struct {
	Data []map[string]any `json:"data"`
	Meta activityListMeta `json:"meta"`
}

func (f *dispatchActivityFixture) list(t *testing.T, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	f.e.dispatchActivityListRoute(w, f.requestAs(f.callerID, http.MethodGet, "/_meta/activity?"+query.Encode(), nil, nil))
	return w
}

func (f *dispatchActivityFixture) listPage(t *testing.T, cursor string, limit int) activityListResponse {
	t.Helper()
	q := url.Values{"model": {activityTestModel}, "record_id": {f.recordID}, "limit": {fmt.Sprint(limit)}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	w := f.list(t, q)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp activityListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	return resp
}

func (f *dispatchActivityFixture) deleteAs(callerID, id string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.e.dispatchActivityDeleteRoute(w, f.requestAs(callerID, http.MethodDelete, "/_meta/activity/"+id, nil, map[string]string{"id": id}))
	return w
}

func TestDispatchActivityCreateRoute_PostsATrimmedCommentAuthoredByTheCaller(t *testing.T) {
	f := newDispatchActivityFixture(t)

	resp := f.postComment(t, f.callerID, "  Customer asked to move delivery to Friday.\n")

	if resp.ID == "" || resp.Kind != "comment" {
		t.Errorf("resp = %+v, want a comment with an id", resp)
	}
	if resp.Body == nil || *resp.Body != "Customer asked to move delivery to Friday." {
		t.Errorf("body = %v, want the trimmed text", resp.Body)
	}
	if resp.Deleted == nil || *resp.Deleted {
		t.Errorf("deleted = %v, want false", resp.Deleted)
	}
	if resp.Author == nil || resp.Author.ID != f.callerID || resp.Author.Name == nil || *resp.Author.Name != "Ama Owusu" {
		t.Errorf("author = %+v, want the caller hydrated with their profile name", resp.Author)
	}
	if resp.Author != nil && resp.Author.AvatarURL != nil {
		t.Errorf("avatar_url = %q, want null for a user with no avatar", *resp.Author.AvatarURL)
	}
}

func TestDispatchActivityListRoute_ReturnsTheFeedWithAuthors(t *testing.T) {
	f := newDispatchActivityFixture(t)
	f.postComment(t, f.callerID, "first")
	f.postComment(t, f.otherCallerID, "second")

	resp := f.listPage(t, "", 20)

	if len(resp.Data) != 2 {
		t.Fatalf("data = %v, want 2 entries", resp.Data)
	}
	if resp.Data[0]["body"] != "second" || resp.Data[1]["body"] != "first" {
		t.Errorf("bodies = %v, %v; want newest first", resp.Data[0]["body"], resp.Data[1]["body"])
	}
	other := resp.Data[0]["author"].(map[string]any)
	if other["id"] != f.otherCallerID || other["name"] != nil || other["avatar_url"] != nil {
		t.Errorf("author = %v, want id %q with null name/avatar_url (no profile)", other, f.otherCallerID)
	}
	if _, ok := resp.Data[0]["changes"]; ok {
		t.Error("a comment entry must not carry a changes key")
	}
	if resp.Meta.HasMore || resp.Meta.Cursor != nil {
		t.Errorf("meta = %+v, want has_more false and a null cursor", resp.Meta)
	}
}

func TestDispatchActivityListRoute_PaginatesEveryEntryExactlyOnceNewestFirst(t *testing.T) {
	f := newDispatchActivityFixture(t)
	const total = 5
	posted := make([]string, total)
	for i := range total {
		posted[i] = f.postComment(t, f.callerID, fmt.Sprintf("comment %d", i)).ID
	}

	var got []string
	cursor := ""
	for page := 0; ; page++ {
		if page > total {
			t.Fatal("pagination did not terminate")
		}
		resp := f.listPage(t, cursor, 2)
		for _, e := range resp.Data {
			got = append(got, e["id"].(string))
		}
		if !resp.Meta.HasMore {
			break
		}
		cursor = *resp.Meta.Cursor
	}

	if len(got) != total {
		t.Fatalf("got %d entries across pages, want %d: %v", len(got), total, got)
	}
	for i, id := range got {
		if want := posted[total-1-i]; id != want {
			t.Errorf("entry %d = %s, want %s", i, id, want)
		}
	}
}

func TestDispatchActivityRoutes_DenyACallerWhoCannotReadTheRecord(t *testing.T) {
	f := newDispatchActivityFixture(t)
	missing := "99999999-9999-9999-9999-999999999999"

	w := f.list(t, url.Values{"model": {activityTestModel}, "record_id": {missing}})
	if w.Code != http.StatusForbidden || decodeErrorCode(t, w) != "permission_denied" {
		t.Errorf("GET status = %d, want 403 permission_denied; body: %s", w.Code, w.Body.String())
	}

	w = f.post(t, f.callerID, map[string]any{"model": activityTestModel, "record_id": missing, "body": "hi"})
	if w.Code != http.StatusForbidden || decodeErrorCode(t, w) != "permission_denied" {
		t.Errorf("POST status = %d, want 403 permission_denied; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchActivityDeleteRoute_SoftDeletesTheAuthorsCommentIdempotently(t *testing.T) {
	f := newDispatchActivityFixture(t)
	comment := f.postComment(t, f.callerID, "oops")

	for i := range 2 {
		if w := f.deleteAs(f.callerID, comment.ID); w.Code != http.StatusNoContent {
			t.Fatalf("delete %d status = %d, want 204; body: %s", i+1, w.Code, w.Body.String())
		}
	}

	resp := f.listPage(t, "", 20)
	if len(resp.Data) != 1 {
		t.Fatalf("data = %v, want the deleted comment still in the feed", resp.Data)
	}
	entry := resp.Data[0]
	if entry["deleted"] != true {
		t.Errorf("deleted = %v, want true", entry["deleted"])
	}
	if _, ok := entry["body"]; ok {
		t.Errorf("entry = %v, want no body key on a deleted comment", entry)
	}
}

func TestDispatchActivityDeleteRoute_RejectsSomeoneElsesComment(t *testing.T) {
	f := newDispatchActivityFixture(t)
	comment := f.postComment(t, f.callerID, "mine")

	w := f.deleteAs(f.otherCallerID, comment.ID)
	if w.Code != http.StatusForbidden || decodeErrorCode(t, w) != "not_author" {
		t.Fatalf("status = %d, want 403 not_author; body: %s", w.Code, w.Body.String())
	}

	entry, err := f.e.recordActivityStore.Get(t.Context(), f.slug, comment.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if entry.DeletedAt != nil {
		t.Error("comment was deleted by a non-author")
	}
}

func TestDispatchActivityDeleteRoute_NotFoundForMissingIDsAndNonComments(t *testing.T) {
	f := newDispatchActivityFixture(t)

	var createdID string
	if err := f.e.primaryDB.QueryRowContext(t.Context(), fmt.Sprintf(
		`INSERT INTO %s.record_activity (model, record_id, kind, author_id) VALUES ($1, $2, 'created', $3) RETURNING id`,
		tenantschema.Name(f.slug),
	), activityTestModel, f.recordID, f.callerID).Scan(&createdID); err != nil {
		t.Fatalf("seed created entry: %v", err)
	}

	for _, id := range []string{"99999999-9999-9999-9999-999999999999", createdID} {
		w := f.deleteAs(f.callerID, id)
		if w.Code != http.StatusNotFound || decodeErrorCode(t, w) != "not_found" {
			t.Errorf("DELETE %s status = %d, want 404 not_found; body: %s", id, w.Code, w.Body.String())
		}
	}
}

func TestDispatchActivityDeleteRoute_DeniesAnAuthorWhoLostReadAccess(t *testing.T) {
	f := newDispatchActivityFixture(t)
	comment := f.postComment(t, f.callerID, "before the record went away")

	if _, err := f.e.primaryDB.ExecContext(t.Context(), fmt.Sprintf(`DELETE FROM %s.widget WHERE id = $1`, tenantschema.Name(f.slug)), f.recordID); err != nil {
		t.Fatalf("delete fixture widget: %v", err)
	}

	w := f.deleteAs(f.callerID, comment.ID)
	if w.Code != http.StatusForbidden || decodeErrorCode(t, w) != "permission_denied" {
		t.Errorf("status = %d, want 403 permission_denied; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchActivityListRoute_RejectsInvalidQueries(t *testing.T) {
	f := newDispatchActivityFixture(t)
	base := func(extra ...string) url.Values {
		q := url.Values{"model": {activityTestModel}, "record_id": {f.recordID}}
		for i := 0; i+1 < len(extra); i += 2 {
			q.Set(extra[i], extra[i+1])
		}
		return q
	}

	cases := map[string]struct {
		query url.Values
		code  string
	}{
		"missing model":     {url.Values{"record_id": {f.recordID}}, "invalid_request"},
		"missing record_id": {url.Values{"model": {activityTestModel}}, "invalid_request"},
		"non-uuid record":   {base("record_id", "abc"), "invalid_request"},
		"malformed cursor":  {base("cursor", "not-a-uuid"), "invalid_request"},
		"limit zero":        {base("limit", "0"), "invalid_request"},
		"limit over max":    {base("limit", "101"), "invalid_request"},
		"limit not integer": {base("limit", "ten"), "invalid_request"},
		"unknown model":     {base("model", "testmodule.nope"), "model_not_found"},
	}
	for name, tc := range cases {
		w := f.list(t, tc.query)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body: %s", name, w.Code, w.Body.String())
			continue
		}
		if code := decodeErrorCode(t, w); code != tc.code {
			t.Errorf("%s: error.code = %q, want %q", name, code, tc.code)
		}
	}
}

func TestDispatchActivityCreateRoute_RejectsInvalidBodies(t *testing.T) {
	f := newDispatchActivityFixture(t)

	for name, text := range map[string]string{
		"empty":       "",
		"whitespace":  " \n\t ",
		"over 10,000": strings.Repeat("é", 10001),
	} {
		w := f.post(t, f.callerID, map[string]any{"model": activityTestModel, "record_id": f.recordID, "body": text})
		if w.Code != http.StatusBadRequest || decodeErrorCode(t, w) != "invalid_request" {
			t.Errorf("%s: status = %d, want 400 invalid_request; body: %s", name, w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	f.e.dispatchActivityCreateRoute(w, f.requestAs(f.callerID, http.MethodPost, "/_meta/activity", []byte("not json"), nil))
	if w.Code != http.StatusBadRequest || decodeErrorCode(t, w) != "invalid_request" {
		t.Errorf("malformed JSON: status = %d, want 400 invalid_request; body: %s", w.Code, w.Body.String())
	}

	if w := f.post(t, f.callerID, map[string]any{"model": activityTestModel, "record_id": f.recordID, "body": strings.Repeat("é", 10000)}); w.Code != http.StatusCreated {
		t.Errorf("10,000-character body: status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchActivityRoutes_RejectVirtualAndTransientModels(t *testing.T) {
	conn := openDispatchORMTestDB(t)
	rt, err := wasm.New(&config.Config{
		CompilationCache:            filepath.Join(t.TempDir(), "cache"),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           1 << 20,
		DBMaxConcurrentTransactions: 10,
	}, conn, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	idField := []model.NamedField{{Name: "id", Def: model.UUID().Required().PrimaryKey()}}
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Name: "testmodule", Type: "connector"},
			ModelDecls: []model.ModelDeclaration{
				{Name: "remote", Backend: model.BackendVirtual, Fields: idField},
				{Name: "wizard", Backend: model.BackendTransient, Fields: idField},
			},
			Capabilities: abi.CapDBRead | abi.CapDBWrite,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}
	e := &Engine{primaryDB: conn, wasmRuntime: rt, moduleRegistry: reg}

	ctx := withTenantContext(t.Context(), &tenantresolve.TenantContext{TenantID: "00000000-0000-0000-0000-000000000001", Slug: "irrelevant"})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "00000000-0000-0000-0000-0000000000aa"})
	recordID := "11111111-1111-1111-1111-111111111111"

	for _, name := range []string{"testmodule.remote", "testmodule.wizard"} {
		w := httptest.NewRecorder()
		e.dispatchActivityListRoute(w, httptest.NewRequest(http.MethodGet, "/_meta/activity?model="+name+"&record_id="+recordID, nil).WithContext(ctx))
		if w.Code != http.StatusBadRequest || decodeErrorCode(t, w) != "activity_unsupported" {
			t.Errorf("GET %s: status = %d, want 400 activity_unsupported; body: %s", name, w.Code, w.Body.String())
		}

		body, _ := json.Marshal(map[string]any{"model": name, "record_id": recordID, "body": "hi"})
		w = httptest.NewRecorder()
		e.dispatchActivityCreateRoute(w, httptest.NewRequest(http.MethodPost, "/_meta/activity", bytes.NewReader(body)).WithContext(ctx))
		if w.Code != http.StatusBadRequest || decodeErrorCode(t, w) != "activity_unsupported" {
			t.Errorf("POST %s: status = %d, want 400 activity_unsupported; body: %s", name, w.Code, w.Body.String())
		}
	}
}
