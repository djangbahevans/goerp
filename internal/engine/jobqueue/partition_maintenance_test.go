package jobqueue_test

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func provisionLedgerTenant(t *testing.T, conn *sql.DB) string {
	t.Helper()
	slug := fmt.Sprintf("ledgermaint%d", time.Now().UnixNano())
	if err := tenantschema.Create(t.Context(), conn, slug); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), conn, slug) })
	if err := enginetables.CreateAll(t.Context(), conn, slug, nil); err != nil {
		t.Fatalf("CreateAll: %v", err)
	}
	return slug
}

func ledgerPartitions(t *testing.T, conn *sql.DB, slug, table string) []string {
	t.Helper()
	rows, err := conn.QueryContext(t.Context(), `
		SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		JOIN pg_namespace n ON n.oid = p.relnamespace
		WHERE n.nspname = $1 AND p.relname = $2 ORDER BY c.relname`, "tenant_"+slug, table)
	if err != nil {
		t.Fatalf("list partitions of %s: %v", table, err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

// defaultLedgerRetention is GOERP_EVENT_LEDGER_RETENTION's default, which the
// engine applies to every tenant anyway.
const defaultLedgerRetention = 840 * time.Hour

func monthSuffix(t time.Time) string { return t.Format("200601") + "01" }

func TestPartitionMaintenance_DropsEventLedgerPartitionsOlderThanTheRetention(t *testing.T) {
	conn := membershiptest.New(t)
	slug := provisionLedgerTenant(t, conn)

	now := time.Now().UTC()
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	old := []time.Time{thisMonth.AddDate(0, -5, 0), thisMonth.AddDate(0, -4, 0), thisMonth.AddDate(0, -3, 0)}
	if _, err := conn.ExecContext(t.Context(), `SELECT partman.create_partition_time($1, $2::timestamptz[])`,
		"tenant_"+slug+".event_deliveries", old); err != nil {
		t.Fatalf("create old partitions: %v", err)
	}
	for _, month := range old {
		if !slices.Contains(ledgerPartitions(t, conn, slug, "event_deliveries"), "event_deliveries_p"+monthSuffix(month)) {
			t.Fatalf("setup: partition for %s was not created", month.Format("2006-01"))
		}
	}

	worker := &jobqueue.PartitionMaintenanceWorker{Pool: conn, EventLedgerRetention: defaultLedgerRetention}
	oldest := "event_deliveries_p" + monthSuffix(old[0])
	var got []string
	// run_maintenance skips its work while another caller holds its advisory
	// lock, which a concurrent test or dev engine can.
	for range 20 {
		if err := worker.Work(t.Context(), nil); err != nil {
			t.Fatalf("Work: %v", err)
		}
		if got = ledgerPartitions(t, conn, slug, "event_deliveries"); !slices.Contains(got, oldest) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	for _, month := range old {
		if slices.Contains(got, "event_deliveries_p"+monthSuffix(month)) {
			t.Errorf("partition for %s survived the 35-day retention", month.Format("2006-01"))
		}
	}
	for _, month := range []time.Time{thisMonth, thisMonth.AddDate(0, 1, 0)} {
		if !slices.Contains(got, "event_deliveries_p"+monthSuffix(month)) {
			t.Errorf("partition for %s was dropped, want it kept", month.Format("2006-01"))
		}
	}
}

func TestPartitionMaintenance_RetentionAppliesOnlyToTheEventLedger(t *testing.T) {
	conn := membershiptest.New(t)
	slug := provisionLedgerTenant(t, conn)

	worker := &jobqueue.PartitionMaintenanceWorker{Pool: conn, EventLedgerRetention: defaultLedgerRetention}
	if err := worker.Work(t.Context(), nil); err != nil {
		t.Fatalf("Work: %v", err)
	}

	retention := func(table string) (value sql.NullString, keepTable bool) {
		t.Helper()
		err := conn.QueryRowContext(t.Context(), `SELECT retention, retention_keep_table FROM partman.part_config WHERE parent_table = $1`,
			"tenant_"+slug+"."+table).Scan(&value, &keepTable)
		if err != nil {
			t.Fatalf("read retention of %s: %v", table, err)
		}
		return value, keepTable
	}
	if value, keepTable := retention("event_deliveries"); value.String != "3024000 seconds" || keepTable {
		t.Errorf("event_deliveries retention = %q keep_table=%v, want 3024000 seconds and no archive table", value.String, keepTable)
	}
	for _, table := range []string{"event_log", "audit_log"} {
		if value, _ := retention(table); value.Valid {
			t.Errorf("%s retention = %q, want none", table, value.String)
		}
	}
}

func TestPartitionMaintenance_WithoutARetentionLeavesTheLedgerAlone(t *testing.T) {
	conn := membershiptest.New(t)
	slug := provisionLedgerTenant(t, conn)

	if err := (&jobqueue.PartitionMaintenanceWorker{Pool: conn}).Work(t.Context(), nil); err != nil {
		t.Fatalf("Work: %v", err)
	}

	var retention sql.NullString
	if err := conn.QueryRowContext(t.Context(), `SELECT retention FROM partman.part_config WHERE parent_table = $1`,
		"tenant_"+slug+".event_deliveries").Scan(&retention); err != nil {
		t.Fatal(err)
	}
	if retention.Valid {
		t.Errorf("retention = %q, want none when the worker has no retention", retention.String)
	}
}

func TestPartitionMaintenance_PurgesExpiredEventLedgerRowsFromTheDefaultPartition(t *testing.T) {
	conn := membershiptest.New(t)
	slug := provisionLedgerTenant(t, conn)
	table := tenantschema.Name(slug) + ".event_deliveries"

	insert := fmt.Sprintf(`INSERT INTO %s (subscriber_module, event_id, emitted_at, event_name, event_version)
		VALUES ('sales', uuidv7(), $1, 'contacts.contact.created', 1)`, table)
	ancient, recent := time.Now().AddDate(-2, 0, 0), time.Now()
	for _, emittedAt := range []time.Time{ancient, recent} {
		if _, err := conn.ExecContext(t.Context(), insert, emittedAt); err != nil {
			t.Fatalf("insert ledger row at %s: %v", emittedAt, err)
		}
	}
	var partition string
	if err := conn.QueryRowContext(t.Context(), `SELECT tableoid::regclass::text FROM `+table+` WHERE emitted_at = $1`, ancient).Scan(&partition); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(partition, "event_deliveries_default") {
		t.Fatalf("setup: the two-year-old row is in %s, want the default partition", partition)
	}

	worker := &jobqueue.PartitionMaintenanceWorker{Pool: conn, EventLedgerRetention: defaultLedgerRetention}
	if err := worker.Work(t.Context(), nil); err != nil {
		t.Fatalf("Work: %v", err)
	}

	var remaining int
	if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Errorf("%d ledger rows remain, want only the recent one", remaining)
	}
}
