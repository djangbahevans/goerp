package engine

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestDispatchShares_CannotListRevokeOrOverwriteAnotherCreatorsGrant(t *testing.T) {
	f := newDispatchSharesFixture(t, model.ReadShare, model.WriteShare)
	sh, _, err := f.e.recordSharesStore.Grant(t.Context(), f.slug, "testmodule.widget", f.recordID, f.recipientID, "read", "00000000-0000-0000-0000-0000000000bb", nil)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchSharesListRoute(w, f.request(http.MethodGet, "/_meta/shares?model=testmodule.widget&record_id="+f.recordID, nil, nil))
	if w.Code != http.StatusOK || w.Body.String() != "{\"data\":[]}" {
		t.Fatalf("foreign grant list = %d %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	f.e.dispatchSharesDeleteRoute(w, f.request(http.MethodDelete, "/_meta/shares/"+sh.ID, nil, map[string]string{"id": sh.ID}))
	if w.Code != http.StatusForbidden || decodeErrorCode(t, w) != "permission_denied" {
		t.Fatalf("foreign grant delete = %d %s", w.Code, w.Body)
	}

	body, err := json.Marshal(shareCreateRequest{Model: "testmodule.widget", RecordID: f.recordID, UserEmail: f.recipientEmail, Permission: "write"})
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	f.e.dispatchSharesCreateRoute(w, f.request(http.MethodPost, "/_meta/shares", body, nil))
	if w.Code != http.StatusForbidden || decodeErrorCode(t, w) != "permission_denied" {
		t.Fatalf("foreign grant overwrite = %d %s", w.Code, w.Body)
	}

	stored, err := f.e.recordSharesStore.Get(t.Context(), f.slug, sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Permission != "read" || stored.SharedBy != sh.SharedBy {
		t.Fatalf("foreign grant changed: %+v", stored)
	}
}
