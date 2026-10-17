package adminsettings

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/auth/authme"
	"github.com/djangbahevans/goerp/internal/engine/auth/tenantcontext"
	"github.com/djangbahevans/goerp/internal/engine/authaudit/audittest"
)

func TestPatch_BrandColorReadEndpointsAndAudit(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin, token := e.member(t, ft, "admin")
	d := e.handler.deps
	me := authme.NewHandler(d.Tenants, e.tenants, d.Auth, e.users, e.roles, d.Files, d.Storage, d.Locales, d.Passwords)
	lookup := tenantcontext.NewHandler(d.Tenants, tenantcontext.Config{
		AppBaseURL: "https://app.goerp.test",
		Policies:   d.Passwords,
		Profiles:   e.tenants,
	})

	assertReads := func(t *testing.T, want any) {
		t.Helper()

		for _, endpoint := range []struct {
			path    string
			handler http.HandlerFunc
			section string
			token   string
		}{
			{"/admin/settings", e.handler.ServeGet, "general", token},
			{"/auth/me", me.ServeHTTP, "tenant", token},
			{"/auth/tenant-context", lookup.ServeHTTP, "tenant", ""},
		} {
			rec := e.do(t, ft, endpoint.token, endpoint.handler, http.MethodGet, endpoint.path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("%s = %d %s", endpoint.path, rec.Code, rec.Body.String())
			}

			body := decode[map[string]any](t, rec)
			got, present := body[endpoint.section].(map[string]any)["brand_color"]
			if !present || got != want {
				t.Errorf("%s brand_color = %v (present=%v), want %v", endpoint.path, got, present, want)
			}
		}
	}

	assertReads(t, nil)

	for _, tc := range []struct {
		name  string
		value any
		want  any
	}{
		{"set", "#0F766E", "#0f766e"},
		{"clear_empty", "", nil},
		{"set_black", "#000000", "#000000"},
		{"clear_null", nil, nil},
		{"set_white", "#FFFFFF", "#ffffff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.patch(t, ft, token, map[string]any{
				"general": map[string]any{"brand_color": tc.value},
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("PATCH = %d %s", rec.Code, rec.Body.String())
			}

			body := decode[map[string]any](t, rec)
			if got, present := body["general"].(map[string]any)["brand_color"]; !present || got != tc.want {
				t.Errorf("PATCH brand_color = %v (present=%v), want %v", got, present, tc.want)
			}

			assertReads(t, tc.want)
			audittest.AssertLatest(t, e.conn, ft.id, "tenant.settings_updated", "", admin)

			var metadata struct {
				Fields []string `json:"fields"`
			}
			if err := json.Unmarshal([]byte(latestAuditMetadata(t, e.conn, ft.id)), &metadata); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(metadata.Fields, []string{"general.brand_color"}) {
				t.Errorf("audit fields = %v, want [general.brand_color]", metadata.Fields)
			}
		})
	}

	rec := e.patch(t, ft, token, map[string]any{"general": map[string]any{"name": "Branded Co"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH omitting brand_color = %d %s", rec.Code, rec.Body.String())
	}

	assertReads(t, "#ffffff")
}

func TestPatch_InvalidBrandColorWritesNothing(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")
	rec := e.patch(t, ft, token, map[string]any{"general": map[string]any{"brand_color": "#0f766e"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("seed brand color = %d %s", rec.Code, rec.Body.String())
	}

	for _, value := range []any{
		"#fff",
		"0f766e",
		"rgb(15,118,110)",
		"#0f766e80",
		"#gggggg",
		" #0f766e",
		"#0f766e\n",
		42,
		false,
		[]string{"#0f766e"},
		map[string]any{},
	} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			rec := e.patch(t, ft, token, map[string]any{
				"general":  map[string]any{"name": "Must not persist", "brand_color": value},
				"security": map[string]any{"password_policy": map[string]any{"min_length": 20}},
			})
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("PATCH = %d %s, want 422", rec.Code, rec.Body.String())
			}

			var body struct {
				Error struct {
					Code    string            `json:"code"`
					Details map[string]string `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != "invalid_setting" || body.Error.Details["field"] != "general.brand_color" {
				t.Errorf("error = %+v", body.Error)
			}

			settings := e.get(t, ft, token)
			if settings.General.Name != "Settings Test Co" || settings.General.BrandColor == nil || *settings.General.BrandColor != "#0f766e" || settings.Security.PasswordPolicy.MinLength != 12 {
				t.Errorf("invalid PATCH changed settings: %+v", settings)
			}

			var audits int
			if err := e.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM system.auth_audit_log WHERE tenant_id = $1 AND event_type = 'tenant.settings_updated'`, ft.id).Scan(&audits); err != nil {
				t.Fatal(err)
			}
			if audits != 1 {
				t.Errorf("audit rows = %d, want only the initial setting", audits)
			}
		})
	}
}
