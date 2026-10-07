package enginetables

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5/pgconn"
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
	conn := membershiptest.New(t)

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
	err := conn.QueryRowContext(t.Context(), fmt.Sprintf(
		`SELECT count(*) FROM %s.notification_templates WHERE is_default AND template_key LIKE 'engine.%%'`, schema)).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Errorf("engine default templates = %d, want 6 (four in_app and the two comment types' email, once each)", n)
	}
}

func TestIsEngineOwned_EventDeliveriesAndItsPartitions(t *testing.T) {
	for _, name := range []string{"event_deliveries", "event_deliveries_p20261001", "event_deliveries_default"} {
		if !IsEngineOwned(name) {
			t.Errorf("IsEngineOwned(%q) = false, want true", name)
		}
	}
	if IsEngineOwned("event_deliveries_archive") {
		t.Error("IsEngineOwned(event_deliveries_archive) = true for a lookalike")
	}
}

func TestCreateAll_EventDeliveriesLedger(t *testing.T) {
	conn := membershiptest.New(t)

	slug := fmt.Sprintf("ledger%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if err := tenantschema.Create(t.Context(), conn, slug); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), conn, slug) })

	for range 2 {
		if err := CreateAll(t.Context(), conn, slug, nil); err != nil {
			t.Fatalf("CreateAll: %v", err)
		}
	}

	var partitioned, registered bool
	var partitions int
	err := conn.QueryRowContext(t.Context(), `
		SELECT c.relkind = 'p',
		       EXISTS (SELECT 1 FROM partman.part_config WHERE parent_table = $1),
		       (SELECT count(*) FROM pg_inherits WHERE inhparent = c.oid)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $2 AND c.relname = 'event_deliveries'`,
		"tenant_"+slug+".event_deliveries", "tenant_"+slug).Scan(&partitioned, &registered, &partitions)
	if err != nil {
		t.Fatalf("inspect event_deliveries: %v", err)
	}
	if !partitioned || !registered || partitions < 4 {
		t.Errorf("event_deliveries partitioned=%v registered with pg_partman=%v partitions=%d, want a registered partitioned table with its partitions", partitioned, registered, partitions)
	}

	insert := fmt.Sprintf(`INSERT INTO %s.event_deliveries (subscriber_module, event_id, emitted_at, event_name, event_version) VALUES ('sales', $1, $2, 'contacts.contact.created', 1)`, schema)
	eventID := "0198a7f0-0000-7000-8000-000000000001"
	for _, emittedAt := range []time.Time{time.Now(), time.Now().AddDate(0, 1, 0)} {
		if _, err := conn.ExecContext(t.Context(), insert, eventID, emittedAt); err != nil {
			t.Fatalf("first delivery at %s: %v", emittedAt, err)
		}
		_, err := conn.ExecContext(t.Context(), insert, eventID, emittedAt)
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "23505" {
			t.Errorf("redelivery at %s = %v, want a unique violation", emittedAt, err)
		}
	}
}
