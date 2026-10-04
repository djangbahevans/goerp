package tenantsync

import (
	"maps"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/enginenotif"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestSyncEngineNotificationTemplates_TenantFailureDoesNotBlockOthers(t *testing.T) {
	conn := membershiptest.New(t)
	ctx := t.Context()
	tenants := tenant.NewStore(conn)
	store := notifications.NewStore(conn)

	for _, slug := range []string{"broken", "healthy"} {
		tn, err := tenants.CreateTenant(ctx, slug, slug)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, "UPDATE system.tenants SET status = 'active' WHERE id = $1", tn.ID); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+tenantschema.Name("healthy")); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapTemplates(ctx, "healthy"); err != nil {
		t.Fatal(err)
	}

	pool := schema.NewPool(conn, time.Second)
	if err := SyncEngineNotificationTemplates(ctx, pool, tenants, 1); err != nil {
		t.Fatalf("sync with a broken tenant: %v", err)
	}

	shipped, err := enginenotif.DefaultRows()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range shipped {
		def, _, err := store.TemplateVersions(ctx, "healthy", row.TemplateKey, row.Channel, row.Locale)
		if err != nil {
			t.Fatal(err)
		}
		if !maps.Equal(def, row.Fields) {
			t.Errorf("healthy tenant default %s/%s/%s = %v, want %v", row.TemplateKey, row.Channel, row.Locale, def, row.Fields)
		}
	}
}
