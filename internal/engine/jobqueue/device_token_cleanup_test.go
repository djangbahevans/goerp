package jobqueue

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func TestDeviceTokenCleanupWorker_DeletesOnlyStaleTokens(t *testing.T) {
	ctx := t.Context()
	conn, err := db.New(inviteExpiryTestDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", inviteExpiryTestDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant.Bootstrap() error: %v", err)
	}
	slug := fmt.Sprintf("devicetokentest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Device Token Cleanup Test")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	schema := tenantschema.Name(slug)
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DELETE FROM system.tenants WHERE id = $1", tt.ID)
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	})
	if _, err := tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	store := notifications.NewStore(conn)
	if err := store.BootstrapDeviceTokens(ctx, slug); err != nil {
		t.Fatalf("BootstrapDeviceTokens() error: %v", err)
	}

	userID := uuid.New().String()
	for token, lastSeen := range map[string]time.Duration{
		"stale":        91 * 24 * time.Hour,
		"almost-stale": 89 * 24 * time.Hour,
		"fresh":        0,
	} {
		_, err := conn.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s.user_device_tokens (tenant_id, user_id, platform, token, last_seen_at)
			VALUES ($1, $2, 'ios', $3, NOW() - $4 * interval '1 second')
		`, schema), tt.ID, userID, token, lastSeen.Seconds())
		if err != nil {
			t.Fatalf("insert token %s: %v", token, err)
		}
	}

	w := &DeviceTokenCleanupWorker{TenantStore: tenantStore, NotificationStore: store}
	if err := w.Work(ctx, &river.Job[DeviceTokenCleanupArgs]{JobRow: &rivertype.JobRow{}, Args: DeviceTokenCleanupArgs{}}); err != nil {
		t.Fatalf("Work() error: %v", err)
	}

	rows, err := conn.QueryContext(ctx, "SELECT token FROM "+schema+".user_device_tokens ORDER BY token")
	if err != nil {
		t.Fatalf("select tokens: %v", err)
	}
	defer rows.Close()
	var left []string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			t.Fatalf("scan token: %v", err)
		}
		left = append(left, token)
	}
	if want := []string{"almost-stale", "fresh"}; !slices.Equal(left, want) {
		t.Errorf("tokens left = %v, want %v", left, want)
	}
}
