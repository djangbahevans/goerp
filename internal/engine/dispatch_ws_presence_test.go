package engine

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/module"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/ws"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestDispatchWSRoute_PresenceProfileAndRecordAdmission(t *testing.T) {
	f := newReadersFixture(t)
	client, err := cache.New(t.Context(), cache.Config{Addr: "localhost:6379", MaxRetries: -1})
	if err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	mod := *f.e.moduleRegistry.Snapshot().Modules()["testmodule"]
	decl := mod.ModelDecls[0]
	decl.EnableOps(model.List, model.Get)
	mod.ModelDecls = []model.ModelDeclaration{decl}
	if _, err := f.e.moduleRegistry.Update(map[string]*module.LoadedModule{"testmodule": &mod}); err != nil {
		t.Fatal(err)
	}
	f.restrictWidgetsToOwner(t)
	f.e.wsHub = ws.NewHub()
	f.e.wsHub.Start(t.Context(), client)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		f.e.wsHub.Close(ctx)
	})
	conn := wsDialAs(t, f.e, f.callerID, &tenantresolve.TenantContext{TenantID: f.tenantID, Slug: f.slug})
	privateID := f.insertWidget(t, "Private", new(f.memberID))
	for _, id := range []string{privateID, f.recordID} {
		channel := "record:" + activityTestModel + ":" + id
		if err := wsjson.Write(t.Context(), conn, map[string]string{"type": "presence.join", "channel": channel}); err != nil {
			t.Fatal(err)
		}
		if id == privateID {
			wsReadType(t, conn, "subscription.denied")
			continue
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		var env struct {
			Type    string `json:"type"`
			Payload struct {
				Users []struct {
					UserID    string  `json:"user_id"`
					Name      string  `json:"name"`
					AvatarURL *string `json:"avatar_url"`
				} `json:"users"`
			} `json:"payload"`
		}
		if err := wsjson.Read(ctx, conn, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type != "presence.update" || len(env.Payload.Users) != 1 || env.Payload.Users[0].UserID != f.callerID || env.Payload.Users[0].Name != "Ama Owusu" || env.Payload.Users[0].AvatarURL != nil {
			t.Fatalf("presence = %+v", env)
		}
	}

	if _, err := f.admin.ExecContext(t.Context(), `DELETE FROM system.user_profiles WHERE user_id = $1`, f.callerID); err != nil {
		t.Fatal(err)
	}
	profile, err := f.e.wsPresenceProfile(t.Context(), f.slug, f.callerID)
	if err != nil || profile.Name != f.recipientEmail || profile.AvatarURL != nil {
		t.Fatalf("email fallback = %+v: %v", profile, err)
	}
}
