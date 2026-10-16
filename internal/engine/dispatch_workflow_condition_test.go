package engine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func conditionalOrderFixture(t *testing.T, condition string) (*dispatchWorkflowFixture, string) {
	t.Helper()
	md := model.Define("order").WithStandardFields().
		Field("name", model.Text().Required().Access(model.AccessRead(perm.Ref("sales:order:name_read")))).
		Field("amount_paid", model.Integer()).
		Field("amount_total", model.Integer()).
		Field("state", model.Selection("draft", "confirmed").Default("draft").Workflow(
			model.Transition("draft", "confirmed", "confirm").Condition(condition),
		))
	f := newDispatchWorkflowFixture(t, *md)
	if _, err := f.e.primaryDB.ExecContext(t.Context(), "ALTER TABLE "+tenantschema.Name(f.slug)+`."order" ADD amount_paid BIGINT, ADD amount_total BIGINT`); err != nil {
		t.Fatal(err)
	}
	f.entryConfirm, _, _, _ = f.e.moduleRegistry.Snapshot().RouteTable().Lookup(http.MethodPost, "/sales/orders/{id}/confirm")
	return f, f.createOrder(t)
}

func TestDispatchORMRoute_WorkflowCondition(t *testing.T) {
	for _, tc := range []struct {
		name      string
		condition string
		paid      any
		status    int
	}{
		{name: "true", condition: "record.amount_paid >= record.amount_total", paid: int64(100), status: http.StatusOK},
		{name: "false", condition: "record.amount_paid >= record.amount_total", paid: int64(99), status: http.StatusConflict},
		{name: "null", condition: "record.amount_paid >= record.amount_total", status: http.StatusConflict},
		{name: "hidden field true", condition: "record.name = 'Order'", status: http.StatusOK},
		{name: "hidden field false", condition: "record.name = 'Other'", status: http.StatusConflict},
		{name: "missing field", condition: "record.missing = 1", status: http.StatusInternalServerError},
		{name: "incompatible types", condition: "record.name > 1", status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, id := conditionalOrderFixture(t, tc.condition)
			table := tenantschema.Name(f.slug) + `."order"`
			if _, err := f.e.primaryDB.ExecContext(t.Context(), "UPDATE "+table+" SET amount_paid = $1, amount_total = 100 WHERE id = $2", tc.paid, id); err != nil {
				t.Fatal(err)
			}
			var beforeEtag string
			if err := f.e.primaryDB.QueryRowContext(t.Context(), "SELECT etag FROM "+table+" WHERE id = $1", id).Scan(&beforeEtag); err != nil {
				t.Fatal(err)
			}

			w := httptest.NewRecorder()
			f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/confirm", nil, f.entryConfirm, map[string]string{"id": id}))
			body := w.Body.String()
			if w.Code != tc.status {
				t.Fatalf("status=%d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if tc.status == http.StatusConflict && (decodeErrorCode(t, w) != abiv1.ErrCodeInvalidTransition || !strings.Contains(body, "confirm condition is not met")) {
				t.Fatalf("condition error = %s", body)
			}
			if tc.status == http.StatusInternalServerError && decodeErrorCode(t, w) != "internal_error" {
				t.Fatalf("evaluation error = %s", body)
			}
			if tc.status == http.StatusOK && strings.Contains(w.Body.String(), `"name"`) {
				t.Fatalf("response exposes a hidden field: %s", w.Body.String())
			}

			var state, etag string
			if err := f.e.primaryDB.QueryRowContext(t.Context(), "SELECT state, etag FROM "+table+" WHERE id = $1", id).Scan(&state, &etag); err != nil {
				t.Fatal(err)
			}
			if tc.status == http.StatusOK {
				if state != "confirmed" || etag == beforeEtag {
					t.Fatalf("successful transition: state=%q etag=%q, previous etag=%q", state, etag, beforeEtag)
				}
			} else if state != "draft" || etag != beforeEtag {
				t.Fatalf("rejected transition changed record: state=%q etag=%q, previous etag=%q", state, etag, beforeEtag)
			}
		})
	}
}

func TestDispatchORMRoute_WorkflowConditionCaller(t *testing.T) {
	for _, tc := range []struct {
		name      string
		condition string
		grant     bool
		allowed   bool
	}{
		{name: "user", condition: "record.created_by = current_user.id", allowed: true},
		{name: "other user", condition: "current_user.id = '00000000-0000-0000-0000-0000000000bb'"},
		{name: "contact", condition: "current_user.contact_id = '00000000-0000-0000-0000-0000000000cc'", allowed: true},
		{name: "tenant", condition: "record.tenant_id = current_user.tenant_id", allowed: true},
		{name: "live role", condition: "user_has_role('manager')", allowed: true},
		{name: "JWT role", condition: "user_has_role('stale')"},
		{name: "permission granted", condition: "user_has_permission('sales:order:confirm')", grant: true, allowed: true},
		{name: "permission denied", condition: "user_has_permission('sales:order:confirm')"},
		{name: "unknown permission", condition: "user_has_permission('sales:order:unknown')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, id := conditionalOrderFixture(t, tc.condition)
			loaded := *f.e.moduleRegistry.Snapshot().Modules()["sales"]
			loaded.Manifest.Permissions = []manifest.Permission{{Name: "sales:order:confirm"}}
			snap, err := f.e.moduleRegistry.Update(map[string]*module.LoadedModule{"sales": &loaded})
			if err != nil {
				t.Fatal(err)
			}
			f.entryConfirm, _, _, _ = snap.RouteTable().Lookup(http.MethodPost, "/sales/orders/{id}/confirm")
			r := f.request(http.MethodPost, "/sales/orders/"+id+"/confirm", nil, f.entryConfirm, map[string]string{"id": id})
			auth := &authcheck.AuthContext{
				IsAuthenticated: true,
				UserID:          "00000000-0000-0000-0000-0000000000aa",
				ContactID:       "00000000-0000-0000-0000-0000000000cc",
				Roles:           []string{"stale"},
				RolesLive:       []string{"manager"},
			}
			if tc.grant {
				index, ok := snap.PermissionRegistry().Index("sales:order:confirm")
				if !ok {
					t.Fatal("fixture permission is not registered")
				}
				auth.PermissionSet.Set(index)
			}
			r = r.WithContext(withAuthContext(r.Context(), auth))

			w := httptest.NewRecorder()
			f.e.dispatchORMRoute(w, r)
			want := http.StatusConflict
			if tc.allowed {
				want = http.StatusOK
			}
			if w.Code != want {
				t.Fatalf("status=%d, want %d: %s", w.Code, want, w.Body.String())
			}
		})
	}
}
