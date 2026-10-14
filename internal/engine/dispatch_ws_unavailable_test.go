package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/ws"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func newUnavailableRecordFixture(t *testing.T, hardDelete bool) (*scheduledActivityFixture, *module.LoadedModule) {
	t.Helper()
	f := newReadersFixture(t)
	ctx := t.Context()
	tt, err := tenant.NewStore(f.admin).CreateTenant(ctx, f.slug, "Record unavailable test")
	if err != nil {
		t.Fatal(err)
	}
	f.tenantID = tt.ID
	t.Cleanup(func() {
		_, _ = f.admin.ExecContext(context.Background(), "DELETE FROM system.tenant_entitlement_overrides WHERE tenant_id=$1", tt.ID)
		_, _ = f.admin.ExecContext(context.Background(), "DELETE FROM system.tenants WHERE id=$1", tt.ID)
	})
	if _, err := f.admin.ExecContext(ctx, "UPDATE "+tenantschema.Name(f.slug)+".widget SET tenant_id=$1", tt.ID); err != nil {
		t.Fatal(err)
	}
	billingStore := billing.NewStore(f.admin)
	if err := billingStore.UpsertEntitlementOverride(ctx, tt.ID, "module.testmodule", "true", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	f.e.tenantResolver = tenantresolve.NewResolver(nil, nil, billingStore)
	mod := *f.e.moduleRegistry.Snapshot().Modules()["testmodule"]
	decl := mod.ModelDecls[0]
	decl.EnableOps(model.List, model.Get)
	if hardDelete {
		decl.Fields = slices.DeleteFunc(slices.Clone(decl.Fields), func(field model.NamedField) bool { return field.Name == "deleted_at" })
	}
	mod.ModelDecls = []model.ModelDeclaration{decl}
	if _, err := f.e.moduleRegistry.Update(map[string]*module.LoadedModule{"testmodule": &mod}); err != nil {
		t.Fatal(err)
	}
	f.e.wsHub = ws.NewHub()
	t.Cleanup(func() { f.e.wsHub.Close(context.Background()) })
	f.e.wasmRuntime.SetRecordChangePublisher(f.e.wsHub)
	return f, &mod
}

func readUnavailable(t *testing.T, conn *websocket.Conn, resource, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var env struct {
		Type    string            `json:"type"`
		Payload map[string]string `json:"payload"`
	}
	if err := wsjson.Read(ctx, conn, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "record.unavailable" || !maps.Equal(env.Payload, map[string]string{"resource": resource, "record_id": id}) {
		t.Fatalf("terminal message = %+v", env)
	}
}

func TestDispatchWSRoute_UnavailableAfterCommittedDelete(t *testing.T) {
	for _, prefix := range []string{"record:", "ui:record:"} {
		for _, hard := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/hard=%t/commit=%t", prefix, hard, commit), func(t *testing.T) {
					f, mod := newUnavailableRecordFixture(t, hard)
					conn := wsDialAs(t, f.e, f.callerID, &tenantresolve.TenantContext{TenantID: f.tenantID, Slug: f.slug})
					channel := prefix + activityTestModel + ":" + f.recordID
					wsSubscribe(t, conn, channel)
					wsSubscribe(t, conn, ws.TenantChannel(f.tenantID))
					wsSubscribe(t, conn, "unknown")
					wsReadType(t, conn, "subscription.denied")
					mc := f.e.newModuleContext(t.Context(), EngineRequest{UserID: f.callerID, TenantID: f.tenantID, TenantSlug: f.slug}, mod)
					defer mc.RollbackAll()
					managed, err := f.e.wasmRuntime.BeginManagedTransaction(t.Context(), mc)
					if err != nil {
						t.Fatal(err)
					}
					defer managed.Rollback()
					if _, hostErr := wasm.ORMUnlink(managed.Context, f.e.wasmRuntime, f.e.primaryDB, f.e.wasmRuntime.EventInsertClient(), nil, mc, abiv1.ORMUnlinkInput{Model: activityTestModel, IDs: []string{f.recordID}, TxID: managed.ID}); hostErr != nil {
						t.Fatal(hostErr)
					}
					if _, err := f.e.wsHub.BroadcastTenant(t.Context(), ws.TenantChannel(f.tenantID), f.tenantID, "before_commit", nil); err != nil {
						t.Fatal(err)
					}
					wsReadType(t, conn, "before_commit")
					if commit {
						if err := managed.Commit(); err != nil {
							t.Fatal(err)
						}
						readUnavailable(t, conn, activityTestModel, f.recordID)
						wsSubscribe(t, conn, channel)
						wsReadType(t, conn, "subscription.denied")
					} else {
						managed.Rollback()
					}
					if _, err := f.e.wsHub.BroadcastTenant(t.Context(), ws.TenantChannel(f.tenantID), f.tenantID, "after_finish", nil); err != nil {
						t.Fatal(err)
					}
					wsReadType(t, conn, "after_finish")
				})
			}
		}
	}
}

func TestDispatchWSRoute_UnavailableEligibility(t *testing.T) {
	for _, outcome := range []string{"row_access", "suspended_user", "suspended_member", "disabled_module", "unregistered_resource", "lookup_error"} {
		t.Run(outcome, func(t *testing.T) {
			f, _ := newUnavailableRecordFixture(t, false)
			f.restrictWidgetsToOwner(t)
			conn := wsDialAs(t, f.e, f.callerID, &tenantresolve.TenantContext{TenantID: f.tenantID, Slug: f.slug})
			channel := "record:" + activityTestModel + ":" + f.recordID
			wsSubscribe(t, conn, channel)
			wsSubscribe(t, conn, ws.TenantChannel(f.tenantID))
			wsSubscribe(t, conn, "unknown")
			wsReadType(t, conn, "subscription.denied")
			schema := tenantschema.Name(f.slug)
			if _, err := f.admin.ExecContext(t.Context(), "UPDATE "+schema+".widget SET internal_ref=$1 WHERE id=$2", f.memberID, f.recordID); err != nil {
				t.Fatal(err)
			}
			switch outcome {
			case "suspended_user":
				if _, err := f.admin.ExecContext(t.Context(), "UPDATE system.users SET status='suspended' WHERE id=$1", f.callerID); err != nil {
					t.Fatal(err)
				}
			case "suspended_member":
				if _, err := f.admin.ExecContext(t.Context(), "UPDATE "+schema+".tenant_members SET status='suspended' WHERE user_id=$1", f.callerID); err != nil {
					t.Fatal(err)
				}
			case "disabled_module":
				if err := billing.NewStore(f.admin).SetModuleEnabledForTenant(t.Context(), f.tenantID, "testmodule", false, nil); err != nil {
					t.Fatal(err)
				}
			case "unregistered_resource":
				mod := *f.e.moduleRegistry.Snapshot().Modules()["testmodule"]
				mod.ModelDecls = nil
				if _, err := f.e.moduleRegistry.Update(map[string]*module.LoadedModule{"testmodule": &mod}); err != nil {
					t.Fatal(err)
				}
			case "lookup_error":
				if _, err := f.admin.ExecContext(t.Context(), "ALTER TABLE "+schema+".widget RENAME TO missing_widget"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.e.wsHub.BroadcastTenant(t.Context(), channel, f.tenantID, "record.updated", map[string]string{"secret": "private"}); err != nil {
				t.Fatal(err)
			}
			if outcome == "row_access" {
				readUnavailable(t, conn, activityTestModel, f.recordID)
			}
			if _, err := f.e.wsHub.BroadcastTenant(t.Context(), ws.TenantChannel(f.tenantID), f.tenantID, "barrier", nil); err != nil {
				t.Fatal(err)
			}
			wsReadType(t, conn, "barrier")
		})
	}
}
