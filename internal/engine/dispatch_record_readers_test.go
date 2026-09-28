package engine

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/user"
)

type recordReadersResponse struct {
	Data []recordReaderResponse `json:"data"`
}

// newReadersFixture is newScheduledActivityFixture with the caller made an
// active member, as a real caller past the auth middleware would be.
func newReadersFixture(t *testing.T) *scheduledActivityFixture {
	t.Helper()
	f := newScheduledActivityFixture(t)
	if _, err := f.admin.ExecContext(t.Context(), `UPDATE system.users SET status = 'active' WHERE id = $1`, f.callerID); err != nil {
		t.Fatalf("activate caller: %v", err)
	}
	if err := f.e.roleStore.AssignRole(t.Context(), f.slug, f.callerID, f.userRoleID, ""); err != nil {
		t.Fatalf("AssignRole() error: %v", err)
	}
	if err := f.e.userStore.EnsureProfile(t.Context(), f.memberID, "Member Default"); err != nil {
		t.Fatalf("EnsureProfile() error: %v", err)
	}
	return f
}

// newNamedUser is newUser with a chosen email local part and profile name
// ("" for none).
func (f *scheduledActivityFixture) newNamedUser(t *testing.T, localPart, name string, status user.Status, member bool) string {
	t.Helper()
	email := fmt.Sprintf("%s.%d@example.com", localPart, time.Now().UnixNano())
	id, err := f.e.userStore.CreateRegistered(t.Context(), email, "", status)
	if err != nil {
		t.Fatalf("CreateRegistered() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.admin.Exec(`DELETE FROM system.users WHERE id = $1`, id) })
	if name != "" {
		if err := f.e.userStore.EnsureProfile(t.Context(), id, name); err != nil {
			t.Fatalf("EnsureProfile() error: %v", err)
		}
	}
	if member {
		if err := f.e.roleStore.AssignRole(t.Context(), f.slug, id, f.userRoleID, ""); err != nil {
			t.Fatalf("AssignRole() error: %v", err)
		}
	}
	return id
}

func (f *scheduledActivityFixture) readers(t *testing.T, callerID, recordID string, params url.Values) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"model": {activityTestModel}, "record_id": {recordID}}
	for k, v := range params {
		q[k] = v
	}
	return f.do(t, callerID, http.MethodGet, "/_meta/record-readers?"+q.Encode(), nil, f.e.dispatchRecordReadersRoute, nil)
}

func (f *scheduledActivityFixture) readerIDs(t *testing.T, callerID, recordID string, params url.Values) []string {
	t.Helper()
	w := f.readers(t, callerID, recordID, params)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp recordReadersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	ids := make([]string, 0, len(resp.Data))
	for _, r := range resp.Data {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestDispatchRecordReaders_SearchesActiveMembersByNameAndEmail(t *testing.T) {
	f := newReadersFixture(t)
	kwame := f.newNamedUser(t, "kwame", "Kwame Mensah", user.StatusActive, true)
	abena := f.newNamedUser(t, "abena", "Abena Serwaa-Boateng", user.StatusActive, true)
	nameless := f.newNamedUser(t, "zz-nameless", "", user.StatusActive, true)
	f.newNamedUser(t, "kofi", "Kofi Outsider", user.StatusActive, false)
	f.newNamedUser(t, "akua", "Akua Suspended", user.StatusSuspended, true)
	f.newNamedUser(t, "adwoa", "Adwoa Invited", user.StatusInvited, true)

	everyone := f.readerIDs(t, f.callerID, f.recordID, url.Values{"limit": {"20"}})
	want := []string{abena, f.callerID, kwame, f.memberID, nameless}
	if !slices.Equal(everyone, want) {
		t.Errorf("empty q = %v, want active members by name, nameless last %v", everyone, want)
	}

	cases := map[string][]string{
		"kw":      {kwame},
		"MEN":     {kwame},
		"serwaa":  {abena},
		"boateng": {abena},
		"am":      {f.callerID},
		"zz-name": {nameless},
		"kofi":    {},
		"akua":    {},
		"%":       {},
		"_":       {},
		"ame":     {},
	}
	for q, want := range cases {
		got := f.readerIDs(t, f.callerID, f.recordID, url.Values{"q": {q}})
		if !slices.Equal(got, want) {
			t.Errorf("q=%q = %v, want %v", q, got, want)
		}
	}

	limited := f.readerIDs(t, f.callerID, f.recordID, url.Values{"limit": {"2"}})
	if !slices.Equal(limited, want[:2]) {
		t.Errorf("limit=2 = %v, want %v", limited, want[:2])
	}

	w := f.readers(t, f.callerID, f.recordID, url.Values{"q": {"kwame"}})
	var resp recordReadersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].Name == nil || *resp.Data[0].Name != "Kwame Mensah" || !strings.HasPrefix(resp.Data[0].Email, "kwame.") {
		t.Errorf("reader = %+v, want Kwame's id, name and email", resp.Data)
	}
}

func TestDispatchRecordReaders_ExcludeSelf(t *testing.T) {
	f := newReadersFixture(t)
	if got := f.readerIDs(t, f.callerID, f.recordID, url.Values{"q": {"ama"}}); !slices.Equal(got, []string{f.callerID}) {
		t.Errorf("without exclude_self = %v, want the caller", got)
	}
	if got := f.readerIDs(t, f.callerID, f.recordID, url.Values{"q": {"ama"}, "exclude_self": {"true"}}); len(got) != 0 {
		t.Errorf("exclude_self=true = %v, want no one", got)
	}
}

func TestDispatchRecordReaders_OnlyReturnsUsersWhoCanReadTheRecord(t *testing.T) {
	f := newReadersFixture(t)
	f.restrictWidgetsToOwner(t)
	private := f.insertWidget(t, "Caller's own", &f.callerID)

	if got := f.readerIDs(t, f.callerID, private, nil); !slices.Equal(got, []string{f.callerID}) {
		t.Errorf("private record readers = %v, want only the caller", got)
	}
	if got := f.readerIDs(t, f.callerID, f.recordID, url.Values{"exclude_self": {"true"}}); !slices.Equal(got, []string{f.memberID}) {
		t.Errorf("public record readers = %v, want the member", got)
	}
	wantError(t, f.readers(t, f.memberID, private, nil), http.StatusForbidden, "permission_denied", "member lists private record's readers")
}

func TestDispatchRecordReaders_StopsAfterFiftyMatchesChecked(t *testing.T) {
	f := newReadersFixture(t)
	f.restrictWidgetsToOwner(t)
	private := f.insertWidget(t, "Caller's own", &f.callerID)
	for i := range recordReadersMaxChecked {
		f.newNamedUser(t, fmt.Sprintf("aa%02d", i), fmt.Sprintf("Aa %02d", i), user.StatusActive, true)
	}

	// Fifty non-readers sort before the caller, so the caller is never
	// reached.
	if got := f.readerIDs(t, f.callerID, private, url.Values{"q": {"a"}}); len(got) != 0 {
		t.Errorf("readers = %v, want none once fifty non-readers were checked", got)
	}
	if got := f.readerIDs(t, f.callerID, private, url.Values{"q": {"ama"}}); !slices.Equal(got, []string{f.callerID}) {
		t.Errorf("narrowed readers = %v, want the caller", got)
	}
}

func TestDispatchRecordReaders_RejectsInvalidRequests(t *testing.T) {
	f := newReadersFixture(t)
	cases := map[string]url.Values{
		"q too long":           {"q": {strings.Repeat("a", 101)}},
		"limit zero":           {"limit": {"0"}},
		"limit over max":       {"limit": {"21"}},
		"limit not a number":   {"limit": {"x"}},
		"exclude_self garbage": {"exclude_self": {"maybe"}},
	}
	for name, params := range cases {
		wantError(t, f.readers(t, f.callerID, f.recordID, params), http.StatusBadRequest, "invalid_request", name)
	}
	if w := f.readers(t, f.callerID, f.recordID, url.Values{"q": {strings.Repeat("a", 100)}}); w.Code != http.StatusOK {
		t.Errorf("100-character q status = %d, want 200", w.Code)
	}
	wantError(t, f.readers(t, f.callerID, "not-a-uuid", nil), http.StatusBadRequest, "invalid_request", "bad record_id")
	wantError(t, f.do(t, f.callerID, http.MethodGet, "/_meta/record-readers?"+url.Values{"model": {"no.such"}, "record_id": {f.recordID}}.Encode(), nil, f.e.dispatchRecordReadersRoute, nil), http.StatusBadRequest, "model_not_found", "unknown model")
}
