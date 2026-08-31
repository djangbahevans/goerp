package schema

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDataMigrationVersion_NoRowReturnsZero(t *testing.T) {
	_, pool := openTestPool(t, 5*time.Second)

	tenantID := uuid.NewString()
	got, err := pool.DataMigrationVersion(context.Background(), tenantID, "nonexistent_module")
	if err != nil {
		t.Fatalf("DataMigrationVersion() error: %v", err)
	}
	if got != "0.0.0" {
		t.Errorf("DataMigrationVersion() = %q, want %q for a tenant/module with no row", got, "0.0.0")
	}
}

func TestDataMigrationVersion_NullColumnReturnsZero(t *testing.T) {
	conn, pool := openTestPool(t, 5*time.Second)

	tenantID := uuid.NewString()
	moduleName := "datamigtest_nullcol"
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, moduleName)
	})

	// RecordSyncSuccess's own upsert never touches data_migration_version,
	// so a tenant/module synced on DDL but never yet reached by a data
	// migration has this row with the column still NULL — the case a
	// fresh sync leaves before any handler has ever run for it.
	sess, err := pool.BeginSync(context.Background(), tenantID, "datamigtestnullcol", moduleName, testManifest("1.0.0"))
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}
	if err := sess.RecordSyncSuccess(context.Background()); err != nil {
		t.Fatalf("RecordSyncSuccess() error: %v", err)
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("session Close() error: %v", err)
	}

	got, err := pool.DataMigrationVersion(context.Background(), tenantID, moduleName)
	if err != nil {
		t.Fatalf("DataMigrationVersion() error: %v", err)
	}
	if got != "0.0.0" {
		t.Errorf("DataMigrationVersion() = %q, want %q for a NULL data_migration_version column", got, "0.0.0")
	}
}

func TestAdvanceDataMigrationVersion_UpdatesWatermark(t *testing.T) {
	conn, pool := openTestPool(t, 5*time.Second)

	tenantID := uuid.NewString()
	moduleName := "datamigtest_advance"
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, moduleName)
	})

	sess, err := pool.BeginSync(context.Background(), tenantID, "datamigtestadvance", moduleName, testManifest("1.4.0"))
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}
	if err := sess.RecordSyncSuccess(context.Background()); err != nil {
		t.Fatalf("RecordSyncSuccess() error: %v", err)
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("session Close() error: %v", err)
	}

	if err := pool.AdvanceDataMigrationVersion(context.Background(), tenantID, moduleName, "1.4.0"); err != nil {
		t.Fatalf("AdvanceDataMigrationVersion() error: %v", err)
	}

	got, err := pool.DataMigrationVersion(context.Background(), tenantID, moduleName)
	if err != nil {
		t.Fatalf("DataMigrationVersion() error: %v", err)
	}
	if got != "1.4.0" {
		t.Errorf("DataMigrationVersion() = %q, want %q after AdvanceDataMigrationVersion", got, "1.4.0")
	}

	var status string
	if err := conn.QueryRow(`SELECT data_migration_status FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, moduleName).Scan(&status); err != nil {
		t.Fatalf("query data_migration_status: %v", err)
	}
	if status != "ok" {
		t.Errorf("data_migration_status = %q, want %q", status, "ok")
	}
}

func TestDataMigrationWatermark_NoRowIsNotEligible(t *testing.T) {
	_, pool := openTestPool(t, 5*time.Second)

	tenantID := uuid.NewString()
	watermark, eligible, err := pool.DataMigrationWatermark(context.Background(), tenantID, "nonexistent_module", "1.0.0")
	if err != nil {
		t.Fatalf("DataMigrationWatermark() error: %v", err)
	}
	if eligible {
		t.Error("eligible = true, want false for a tenant/module that has never synced")
	}
	if watermark != "0.0.0" {
		t.Errorf("watermark = %q, want %q", watermark, "0.0.0")
	}
}

func TestDataMigrationWatermark_StaleCurrentVersionIsNotEligible(t *testing.T) {
	conn, pool := openTestPool(t, 5*time.Second)

	tenantID := uuid.NewString()
	moduleName := "datamigtest_stale"
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, moduleName)
	})

	sess, err := pool.BeginSync(context.Background(), tenantID, "datamigteststale", moduleName, testManifest("1.4.0"))
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}
	if err := sess.RecordSyncSuccess(context.Background()); err != nil {
		t.Fatalf("RecordSyncSuccess() error: %v", err)
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("session Close() error: %v", err)
	}

	// Synced to 1.4.0, but a migration targeting 1.5.0 shouldn't be
	// considered eligible yet — this tenant hasn't reached that version.
	_, eligible, err := pool.DataMigrationWatermark(context.Background(), tenantID, moduleName, "1.5.0")
	if err != nil {
		t.Fatalf("DataMigrationWatermark() error: %v", err)
	}
	if eligible {
		t.Error("eligible = true, want false when current_version hasn't reached targetVersion")
	}
}

func TestDataMigrationWatermark_FailedSyncIsNotEligible(t *testing.T) {
	conn, pool := openTestPool(t, 5*time.Second)

	tenantID := uuid.NewString()
	moduleName := "datamigtest_failed"
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, moduleName)
	})

	sess, err := pool.BeginSync(context.Background(), tenantID, "datamigtestfailed", moduleName, testManifest("1.4.0"))
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}
	if err := sess.RecordSyncSuccess(context.Background()); err != nil {
		t.Fatalf("RecordSyncSuccess() error: %v", err)
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("session Close() error: %v", err)
	}
	// A later sync attempt (still targeting the same 1.4.0) failed —
	// current_version matches, but schema_sync_status doesn't say "ok".
	if _, err := conn.Exec(`UPDATE system.module_schema_versions SET schema_sync_status = 'failed' WHERE tenant_id = $1 AND module_name = $2`, tenantID, moduleName); err != nil {
		t.Fatalf("seed failed sync status: %v", err)
	}

	_, eligible, err := pool.DataMigrationWatermark(context.Background(), tenantID, moduleName, "1.4.0")
	if err != nil {
		t.Fatalf("DataMigrationWatermark() error: %v", err)
	}
	if eligible {
		t.Error("eligible = true, want false when schema_sync_status is not ok")
	}
}
