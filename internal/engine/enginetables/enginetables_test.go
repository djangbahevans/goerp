package enginetables

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestIsEngineOwned_NotificationTables(t *testing.T) {
	for _, name := range []string{"notifications", "notification_deliveries", "notification_preferences", "user_device_tokens"} {
		if !IsEngineOwned(name) {
			t.Errorf("IsEngineOwned(%q) = false, want true", name)
		}
	}
	if IsEngineOwned("notification_deliveries_archive") {
		t.Error("IsEngineOwned(notification_deliveries_archive) = true for a non-partitioned table's lookalike")
	}
}

func TestCreateAll_SeedsTheEngineTypesDefaultTemplates(t *testing.T) {
	conn, err := db.New("postgres://goerp:dev@localhost:15432/goerp")
	if err != nil {
		t.Skipf("postgres not reachable (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug := fmt.Sprintf("enginetpl%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if err := tenantschema.Create(t.Context(), conn, slug); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), conn, slug) })

	for range 2 {
		if err := CreateAll(t.Context(), conn, slug, []string{"en"}); err != nil {
			t.Fatalf("CreateAll() error: %v", err)
		}
	}

	var n int
	err = conn.QueryRowContext(t.Context(), fmt.Sprintf(
		`SELECT count(*) FROM %s.notification_templates WHERE is_default AND template_key LIKE 'engine.%%'`, schema)).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Errorf("engine default templates = %d, want 6 (four in_app and the two comment types' email, once each)", n)
	}
}
