package authaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance (bypassing PgBouncer), same convention as tenant.Store's own
// tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func openTestStore(t *testing.T) (*Store, *tenant.Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("tenant.Bootstrap() error: %v", err)
	}

	store := NewStore(conn, tenantStore)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return store, tenantStore, conn
}

// uniqueSlug keeps each test's inserted tenant row from colliding with a
// previous run's leftovers or a concurrently-running test — mirrors
// tenant package's own test helper (unexported there, so not reusable
// directly).
func uniqueSlug(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("t%d", time.Now().UnixNano())
}

func createTenant(t *testing.T, tenantStore *tenant.Store, conn *sql.DB, slug string) *tenant.Tenant {
	t.Helper()
	tt, err := tenantStore.CreateTenant(context.Background(), slug, "Auth Audit Test")
	if err != nil {
		t.Fatalf("CreateTenant(%q) error: %v", slug, err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DELETE FROM system.tenants WHERE id = $1", tt.ID)
	})
	return tt
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, _, _ := openTestStore(t)

	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

func TestBootstrap_CreatesPartitionedTableAndIndex(t *testing.T) {
	_, _, conn := openTestStore(t)
	ctx := context.Background()

	var partitionStrategy string
	err := conn.QueryRowContext(ctx,
		`SELECT partstrat FROM pg_partitioned_table pt
		 JOIN pg_class c ON c.oid = pt.partrelid
		 WHERE c.relname = 'auth_audit_log'`,
	).Scan(&partitionStrategy)
	if err != nil {
		t.Fatalf("expected auth_audit_log to be a partitioned table: %v", err)
	}
	if partitionStrategy != "r" {
		t.Errorf("partition strategy = %q, want %q (range)", partitionStrategy, "r")
	}

	var indexDef string
	err = conn.QueryRowContext(ctx,
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'idx_auth_audit_log_time'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected idx_auth_audit_log_time to exist: %v", err)
	}
	if indexDef == "" {
		t.Fatal("expected a non-empty index definition")
	}

	var registered bool
	err = conn.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM partman.part_config WHERE parent_table = 'system.auth_audit_log')`,
	).Scan(&registered)
	if err != nil {
		t.Fatalf("check partman registration: %v", err)
	}
	if !registered {
		t.Error("expected system.auth_audit_log to be registered with pg_partman")
	}
}

func TestInsert_RoundTripsAllFields(t *testing.T) {
	store, tenantStore, conn := openTestStore(t)
	ctx := context.Background()
	tt := createTenant(t, tenantStore, conn, uniqueSlug(t))

	metadata, err := json.Marshal(map[string]any{"reason": "test"})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	row := Row{
		EventType:     "login.failure",
		TenantID:      tt.ID,
		IPAddress:     "203.0.113.5",
		UserAgent:     "test-agent",
		CountryCode:   "US",
		Success:       false,
		FailureReason: "bad_password",
		Metadata:      metadata,
	}
	if err := store.Insert(ctx, row); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}

	var gotIPAddress, gotUserAgent, gotCountryCode, gotFailureReason string
	var gotSuccess bool
	var gotMetadata []byte
	err = conn.QueryRowContext(ctx, `
		SELECT host(ip_address), user_agent, country_code, success, failure_reason, metadata
		FROM system.auth_audit_log
		WHERE event_type = $1 AND tenant_id = $2
	`, row.EventType, row.TenantID).Scan(&gotIPAddress, &gotUserAgent, &gotCountryCode, &gotSuccess, &gotFailureReason, &gotMetadata)
	if err != nil {
		t.Fatalf("query inserted row: %v", err)
	}

	if gotIPAddress != row.IPAddress || gotUserAgent != row.UserAgent || gotCountryCode != row.CountryCode {
		t.Errorf("ip/agent/country = %q/%q/%q, want %q/%q/%q", gotIPAddress, gotUserAgent, gotCountryCode, row.IPAddress, row.UserAgent, row.CountryCode)
	}
	if gotSuccess != row.Success || gotFailureReason != row.FailureReason {
		t.Errorf("success/failure_reason = %v/%q, want %v/%q", gotSuccess, gotFailureReason, row.Success, row.FailureReason)
	}
	var gotPayload, wantPayload map[string]any
	if err := json.Unmarshal(gotMetadata, &gotPayload); err != nil {
		t.Fatalf("unmarshal got metadata: %v", err)
	}
	if err := json.Unmarshal(row.Metadata, &wantPayload); err != nil {
		t.Fatalf("unmarshal want metadata: %v", err)
	}
	if gotPayload["reason"] != wantPayload["reason"] {
		t.Errorf("metadata = %+v, want %+v", gotPayload, wantPayload)
	}
}

func TestInsert_OptionalColumnsStoreAsNull(t *testing.T) {
	store, tenantStore, conn := openTestStore(t)
	ctx := context.Background()
	tt := createTenant(t, tenantStore, conn, uniqueSlug(t))

	row := Row{EventType: "role.granted", TenantID: tt.ID, Success: true}
	if err := store.Insert(ctx, row); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}

	var userID, sessionID, apiKeyID, ipAddress sql.NullString
	err := conn.QueryRowContext(ctx, `
		SELECT user_id, session_id, api_key_id, ip_address
		FROM system.auth_audit_log
		WHERE event_type = $1 AND tenant_id = $2
	`, row.EventType, row.TenantID).Scan(&userID, &sessionID, &apiKeyID, &ipAddress)
	if err != nil {
		t.Fatalf("query inserted row: %v", err)
	}
	if userID.Valid || sessionID.Valid || apiKeyID.Valid || ipAddress.Valid {
		t.Errorf("expected user_id/session_id/api_key_id/ip_address all NULL, got %+v/%+v/%+v/%+v", userID, sessionID, apiKeyID, ipAddress)
	}
}

func TestEmit_ResolvesTenantAndWritesRow(t *testing.T) {
	store, tenantStore, conn := openTestStore(t)
	ctx := context.Background()
	slug := uniqueSlug(t)
	tt := createTenant(t, tenantStore, conn, slug)

	err := store.Emit(ctx, slug, "user.invited", map[string]any{"invitation_id": "abc-123", "email": "a@example.com"})
	if err != nil {
		t.Fatalf("Emit() error: %v", err)
	}

	var gotTenantID string
	var gotSuccess bool
	var gotMetadata []byte
	err = conn.QueryRowContext(ctx, `
		SELECT tenant_id, success, metadata
		FROM system.auth_audit_log
		WHERE event_type = 'user.invited' AND tenant_id = $1
	`, tt.ID).Scan(&gotTenantID, &gotSuccess, &gotMetadata)
	if err != nil {
		t.Fatalf("query inserted row: %v", err)
	}
	if gotTenantID != tt.ID {
		t.Errorf("tenant_id = %q, want %q", gotTenantID, tt.ID)
	}
	if !gotSuccess {
		t.Error("expected success = true for an invite event")
	}

	var payload map[string]any
	if err := json.Unmarshal(gotMetadata, &payload); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if payload["invitation_id"] != "abc-123" || payload["email"] != "a@example.com" {
		t.Errorf("metadata = %+v, want invitation_id=abc-123 email=a@example.com", payload)
	}
}

func TestEmit_UnknownTenantSlugFails(t *testing.T) {
	store, _, _ := openTestStore(t)

	err := store.Emit(context.Background(), "does-not-exist-"+uniqueSlug(t), "user.invited", nil)
	if err == nil {
		t.Fatal("expected an error resolving an unknown tenant slug, got nil")
	}
}
