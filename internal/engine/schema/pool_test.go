package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// TestBootstrap_ConcurrentCallsAllSucceed guards against goerp#171: two
// concurrent Bootstrap calls racing on CREATE TABLE/SCHEMA IF NOT EXISTS
// could previously fail one of them with a raw Postgres constraint
// violation instead of behaving as the idempotent no-op IF NOT EXISTS is
// supposed to guarantee. openTestPool's own Bootstrap call already
// created system.module_schema_versions, so this exercises the
// steady-state case every engine replica after the first hits on
// restart, not the very first table-creation race itself — see
// db.TestWithAdvisoryLock_SerializesConcurrentHoldersOfSameKey for a
// direct test of the underlying locking mechanism against a table that
// doesn't exist yet.
func TestBootstrap_ConcurrentCallsAllSucceed(t *testing.T) {
	_, pool := openTestPool(t, 5*time.Second)

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			errs <- pool.Bootstrap(context.Background())
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Bootstrap() error: %v", err)
		}
	}
}

func TestStatusForTenant_ReturnsRowsOrderedByModuleName(t *testing.T) {
	_, pool := openTestPool(t, 5*time.Second)

	const tenantID = "66666666-6666-6666-6666-666666666666"

	cleanup, err := db.New(localSchemaSyncDSN)
	if err != nil {
		t.Fatalf("db.New() for cleanup connection: %v", err)
	}
	defer func() { _ = cleanup.Close() }()
	if _, err := cleanup.Exec("DELETE FROM system.module_schema_versions WHERE tenant_id = $1", tenantID); err != nil {
		t.Fatalf("reset module_schema_versions: %v", err)
	}
	t.Cleanup(func() {
		_, _ = cleanup.Exec("DELETE FROM system.module_schema_versions WHERE tenant_id = $1", tenantID)
	})

	rows := []struct {
		module, version, status string
		synced                  bool
	}{
		{"sales", "1.0.0", "ok", true},
		{"contacts", "2.0.0", "ok", true},
		{"hr", "1.0.0", "failed", false},
	}
	for _, r := range rows {
		var syncedAtExpr string
		if r.synced {
			syncedAtExpr = "NOW()"
		} else {
			syncedAtExpr = "NULL"
		}
		if _, err := cleanup.ExecContext(context.Background(),
			`INSERT INTO system.module_schema_versions (tenant_id, module_name, current_version, schema_sync_status, schema_synced_at)
			 VALUES ($1, $2, $3, $4, `+syncedAtExpr+`)`,
			tenantID, r.module, r.version, r.status,
		); err != nil {
			t.Fatalf("insert fixture row for %q: %v", r.module, err)
		}
	}

	got, err := pool.StatusForTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("StatusForTenant() error: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}

	// ORDER BY module_name: contacts, hr, sales
	wantOrder := []string{"contacts", "hr", "sales"}
	for i, name := range wantOrder {
		if got[i].ModuleName != name {
			t.Errorf("got[%d].ModuleName = %q, want %q", i, got[i].ModuleName, name)
		}
	}

	okCount := 0
	for _, s := range got {
		if s.Status == "ok" {
			okCount++
			if s.SyncedAt == nil {
				t.Errorf("module %q has status ok but nil SyncedAt", s.ModuleName)
			}
		}
	}
	if okCount != 2 {
		t.Errorf("okCount = %d, want 2", okCount)
	}
}

func TestStatusFiltered_IncludesDataMigrationFields(t *testing.T) {
	_, pool := openTestPool(t, 5*time.Second)

	const tenantID = "77777777-7777-7777-7777-777777777777"

	cleanup, err := db.New(localSchemaSyncDSN)
	if err != nil {
		t.Fatalf("db.New() for cleanup connection: %v", err)
	}
	defer func() { _ = cleanup.Close() }()
	if _, err := cleanup.Exec("DELETE FROM system.module_schema_versions WHERE tenant_id = $1", tenantID); err != nil {
		t.Fatalf("reset module_schema_versions: %v", err)
	}
	t.Cleanup(func() {
		_, _ = cleanup.Exec("DELETE FROM system.module_schema_versions WHERE tenant_id = $1", tenantID)
	})
	if _, err := cleanup.Exec(`
		INSERT INTO system.tenants (id, slug, name, plan, status, region, created_at)
		VALUES ($1, 'datamigfiltered', 'Data Migration Filtered', 'pro', 'active', 'default', NOW())
		ON CONFLICT (id) DO NOTHING
	`, tenantID); err != nil {
		t.Fatalf("insert fixture tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = cleanup.Exec("DELETE FROM system.tenants WHERE id = $1", tenantID)
	})

	if _, err := cleanup.ExecContext(context.Background(),
		`INSERT INTO system.module_schema_versions
		 (tenant_id, module_name, current_version, schema_sync_status, schema_synced_at, data_migration_version, data_migration_status)
		 VALUES ($1, 'sales', '1.3.0', 'ok', NOW(), '1.3.0', 'running')`,
		tenantID,
	); err != nil {
		t.Fatalf("insert fixture row: %v", err)
	}

	got, err := pool.StatusFiltered(context.Background(), "datamigfiltered", "", "")
	if err != nil {
		t.Fatalf("StatusFiltered() error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].DataMigrationVersion != "1.3.0" {
		t.Errorf("DataMigrationVersion = %q, want %q", got[0].DataMigrationVersion, "1.3.0")
	}
	if got[0].DataMigrationStatus != "running" {
		t.Errorf("DataMigrationStatus = %q, want %q", got[0].DataMigrationStatus, "running")
	}
}

func TestStatusForTenant_NoRowsReturnsEmpty(t *testing.T) {
	_, pool := openTestPool(t, 5*time.Second)

	got, err := pool.StatusForTenant(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("StatusForTenant() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

// TestModuleSyncStatus_MarshalsSnakeCase guards against ModuleSyncStatus
// silently losing its json tags again — GET /admin/tenants/{slug} embeds
// this type directly as its modules array, and every other field in that
// response (schema_table_count, modules_synced, admin_user, ...) is
// snake_case, so a regression here would produce a response with one
// inconsistently-cased array of objects.
func TestModuleSyncStatus_MarshalsSnakeCase(t *testing.T) {
	syncedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	status := ModuleSyncStatus{
		ModuleName:     "contacts",
		CurrentVersion: "1.0.0",
		Status:         "ok",
		SyncedAt:       &syncedAt,
	}

	got, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	for _, key := range []string{"module_name", "current_version", "status", "synced_at"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("marshaled JSON %s missing snake_case key %q", got, key)
		}
	}
	for _, key := range []string{"ModuleName", "CurrentVersion", "Status", "SyncedAt"} {
		if _, ok := decoded[key]; ok {
			t.Errorf("marshaled JSON %s has PascalCase key %q, want snake_case only", got, key)
		}
	}
}

func TestTableCount_CountsTablesInTenantSchema(t *testing.T) {
	conn, pool := openTestPool(t, 5*time.Second)

	slug := fmt.Sprintf("tablecount%d", time.Now().UnixNano())
	schemaName := `"tenant_` + slug + `"`

	if _, err := conn.ExecContext(context.Background(), "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
	})

	for _, table := range []string{"widgets", "gadgets"} {
		if _, err := conn.ExecContext(context.Background(),
			fmt.Sprintf("CREATE TABLE %s.%s (id UUID PRIMARY KEY)", schemaName, table),
		); err != nil {
			t.Fatalf("create fixture table %q: %v", table, err)
		}
	}

	got, err := pool.TableCount(context.Background(), slug)
	if err != nil {
		t.Fatalf("TableCount() error: %v", err)
	}
	if got != 2 {
		t.Errorf("TableCount() = %d, want 2", got)
	}
}

func TestTableCount_UnknownTenantReturnsZero(t *testing.T) {
	_, pool := openTestPool(t, 5*time.Second)

	got, err := pool.TableCount(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("TableCount() error: %v", err)
	}
	if got != 0 {
		t.Errorf("TableCount() = %d, want 0", got)
	}
}
