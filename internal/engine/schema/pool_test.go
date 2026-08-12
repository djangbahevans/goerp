package schema

import (
	"context"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

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
