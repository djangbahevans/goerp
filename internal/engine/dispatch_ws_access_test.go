package engine

import (
	"context"
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/ws"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func wsSubscribe(t *testing.T, conn *websocket.Conn, channel string) {
	t.Helper()
	if err := wsjson.Write(t.Context(), conn, map[string]string{"type": "subscribe", "channel": channel}); err != nil {
		t.Fatal(err)
	}
}

func wsReadType(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var env struct {
		Type string `json:"type"`
	}
	if err := wsjson.Read(ctx, conn, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != want {
		t.Fatalf("message type = %s, want %s", env.Type, want)
	}
}

func wsDialAs(t *testing.T, e *Engine, userID string, tenantCtx *tenantresolve.TenantContext) *websocket.Conn {
	t.Helper()
	url := wsTestServer(t, e, &authcheck.AuthContext{IsAuthenticated: true, UserID: userID}, tenantCtx)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func TestDispatchWSRoute_FeedUsesCurrentEntitlementsAndListPermissions(t *testing.T) {
	f := newChainFixture(t)
	const (
		readPermission  = "widgets:widget:custom_read"
		extraPermission = "widgets:widget:extra_read"
	)
	reg := &registry.ModuleRegistry{}
	mod := &module.LoadedModule{
		Status: module.StatusReady,
		Manifest: manifest.Manifest{
			Name: "widgets",
			Type: "standard",
			Permissions: []manifest.Permission{
				{Name: readPermission},
				{Name: extraPermission},
				{Name: chainTestPermission},
			},
		},
		ModelDecls: []model.ModelDeclaration{
			*model.Define("widget"),
			*model.Define("public"),
			*model.Define("raw"),
		},
		ExplicitRoutes: []abiv1.RouteDeclaration{
			{Name: "list", Model: "widgets.widget", CRUDAction: "list", Permissions: []string{readPermission, extraPermission}},
			{Name: "list", Model: "widgets.public", CRUDAction: "list"},
			{Method: "GET", Path: "/raw", Model: "widgets.raw", CRUDAction: "list"},
		},
	}
	if _, err := reg.Update(map[string]*module.LoadedModule{"widgets": mod}); err != nil {
		t.Fatal(err)
	}
	roles := role.NewStore(f.conn)
	roleID, err := roles.GetRoleByName(t.Context(), f.tenantSlug, "admin")
	if err != nil {
		t.Fatal(err)
	}
	bits := permcache.NewRolePermissionMap()
	refresh := func() {
		t.Helper()
		if err := bits.RebuildTenant(t.Context(), roles, reg.Snapshot().PermissionRegistry, f.tenantSlug); err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	billingStore := billing.NewStore(f.conn)
	e := &Engine{
		wsHub:             ws.NewHub(),
		moduleRegistry:    reg,
		userStore:         user.NewStore(f.conn),
		roleStore:         roles,
		rolePermissionMap: bits,
		tenantResolver:    tenantresolve.NewResolver(nil, nil, billingStore),
	}
	conn := wsDialAs(t, e, f.userID, &tenantresolve.TenantContext{TenantID: f.tenantID, Slug: f.tenantSlug})
	const channel = "feed:widgets"
	wsSubscribe(t, conn, channel)
	wsSubscribe(t, conn, "feed:unknown")
	wsReadType(t, conn, "subscription.denied")
	broadcast := func(resource string, want int) {
		t.Helper()
		if n, err := e.wsHub.BroadcastTenant(t.Context(), channel, f.tenantID, "records.changed", map[string]string{"resource": resource}); err != nil || n != want {
			t.Fatalf("resource %s reached %d, want %d: %v", resource, n, want, err)
		}
		if want > 0 {
			wsReadType(t, conn, "records.changed")
		}
	}
	broadcast("widgets.widget", 0)
	broadcast("widgets.raw", 0)
	broadcast("other.widget", 0)
	broadcast("widgets.public", 1)
	schema := tenantschema.Name(f.tenantSlug)
	if _, err := f.conn.ExecContext(t.Context(), "INSERT INTO "+schema+".role_permissions (role_id, permission_name) VALUES ($1, $2)", roleID, readPermission); err != nil {
		t.Fatal(err)
	}
	refresh()
	broadcast("widgets.widget", 0)
	if _, err := f.conn.ExecContext(t.Context(), "INSERT INTO "+schema+".role_permissions (role_id, permission_name) VALUES ($1, $2)", roleID, extraPermission); err != nil {
		t.Fatal(err)
	}
	refresh()
	broadcast("widgets.widget", 1)
	if _, err := f.conn.ExecContext(t.Context(), "DELETE FROM "+schema+".role_permissions WHERE role_id = $1 AND permission_name = $2", roleID, readPermission); err != nil {
		t.Fatal(err)
	}
	refresh()
	broadcast("widgets.widget", 0)
	broadcast("widgets.public", 1)
	if _, err := f.conn.ExecContext(t.Context(), "UPDATE "+schema+".tenant_members SET status = 'suspended' WHERE user_id = $1", f.userID); err != nil {
		t.Fatal(err)
	}
	broadcast("widgets.public", 0)
	if _, err := f.conn.ExecContext(t.Context(), "UPDATE "+schema+".tenant_members SET status = 'active' WHERE user_id = $1", f.userID); err != nil {
		t.Fatal(err)
	}
	broadcast("widgets.public", 1)
	if err := billingStore.SetModuleEnabledForTenant(t.Context(), f.tenantID, "widgets", false, nil); err != nil {
		t.Fatal(err)
	}
	broadcast("widgets.public", 0)
	wsSubscribe(t, conn, channel)
	wsReadType(t, conn, "subscription.denied")
	if err := billingStore.SetModuleEnabledForTenant(t.Context(), f.tenantID, "widgets", true, nil); err != nil {
		t.Fatal(err)
	}
	broadcast("widgets.public", 0)
}

func TestDispatchWSRoute_RecordChecksLiveReadAccess(t *testing.T) {
	for _, prefix := range []string{"record:", "ui:record:"} {
		t.Run(prefix, func(t *testing.T) {
			f := newReadersFixture(t)
			mod := *f.e.moduleRegistry.Snapshot().Modules()["testmodule"]
			decl := mod.ModelDecls[0]
			decl.EnableOps(model.List, model.Get)
			mod.ModelDecls = []model.ModelDeclaration{
				decl,
				*model.Define("hidden", model.Table("widget")).WithStandardFields().Field("name", model.Text()),
			}
			if _, err := f.e.moduleRegistry.Update(map[string]*module.LoadedModule{"testmodule": &mod}); err != nil {
				t.Fatal(err)
			}
			f.restrictWidgetsToOwner(t)
			f.e.wsHub = ws.NewHub()
			conn := wsDialAs(t, f.e, f.callerID, &tenantresolve.TenantContext{TenantID: f.tenantID, Slug: f.slug})
			privateID := f.insertWidget(t, "Other member's record", new(f.memberID))
			foreignSlug := fmt.Sprintf("wsforeign%d", time.Now().UnixNano())
			createFixtureWidgetsSchema(t, f.admin, foreignSlug)
			foreignID := uuid.New().String()
			if _, err := f.admin.ExecContext(t.Context(), "INSERT INTO "+tenantschema.Name(foreignSlug)+".widget (id, tenant_id, name) VALUES ($1, $2, 'Foreign')", foreignID, uuid.New().String()); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{
				activityTestModel + ":" + privateID,
				activityTestModel + ":" + foreignID,
				"testmodule.unknown:" + f.recordID,
				"testmodule.hidden:" + f.recordID,
			} {
				wsSubscribe(t, conn, prefix+target)
				wsReadType(t, conn, "subscription.denied")
			}
			channel := prefix + activityTestModel + ":" + f.recordID
			wsSubscribe(t, conn, channel)
			wsSubscribe(t, conn, "unknown")
			wsReadType(t, conn, "subscription.denied")
			if n, err := f.e.wsHub.BroadcastTenant(t.Context(), channel, f.tenantID, "before", nil); err != nil || n != 1 {
				t.Fatalf("readable record reached %d: %v", n, err)
			}
			wsReadType(t, conn, "before")
			if _, err := f.admin.ExecContext(t.Context(), "UPDATE "+tenantschema.Name(f.slug)+".widget SET internal_ref = $1 WHERE id = $2", f.memberID, f.recordID); err != nil {
				t.Fatal(err)
			}
			if n, err := f.e.wsHub.BroadcastTenant(t.Context(), channel, f.tenantID, "after", nil); err != nil || n != 0 {
				t.Fatalf("inaccessible record reached %d: %v", n, err)
			}
			wsSubscribe(t, conn, channel)
			wsReadType(t, conn, "subscription.denied")
			if _, err := f.admin.ExecContext(t.Context(), "UPDATE "+tenantschema.Name(f.slug)+".widget SET internal_ref = NULL WHERE id = $1", f.recordID); err != nil {
				t.Fatal(err)
			}
			if n, err := f.e.wsHub.BroadcastTenant(t.Context(), channel, f.tenantID, "restored", nil); err != nil || n != 0 {
				t.Fatalf("removed subscription reached %d: %v", n, err)
			}
		})
	}
}
