package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/google/uuid"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance (bypassing PgBouncer), same convention as auditlog.Store's
// tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func openTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	store := NewStore(conn)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return store, conn
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, _ := openTestStore(t)

	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

// TestBootstrap_ConcurrentCallsAllSucceed guards against goerp#171 — see
// schema.TestBootstrap_ConcurrentCallsAllSucceed's doc comment for what
// this does and doesn't prove.
func TestBootstrap_ConcurrentCallsAllSucceed(t *testing.T) {
	store, _ := openTestStore(t)

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			errs <- store.Bootstrap(context.Background())
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

func TestBootstrap_CreatesSessionsTableWithAllColumns(t *testing.T) {
	_, conn := openTestStore(t)

	wantColumns := []string{
		"id", "user_id", "tenant_id", "family_id", "device_id", "refresh_hash",
		"user_agent", "ip_address", "country_code", "created_at", "last_active_at",
		"expires_at", "rotated_at", "revoked_at", "revoke_reason",
		"mfa_verified_at", "mfa_method", "mfa_credential_id",
	}
	for _, col := range wantColumns {
		var exists bool
		err := conn.QueryRowContext(context.Background(), `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'system' AND table_name = 'sessions' AND column_name = $1
			)
		`, col).Scan(&exists)
		if err != nil {
			t.Fatalf("query column %q: %v", col, err)
		}
		if !exists {
			t.Errorf("expected column %q to exist on system.sessions", col)
		}
	}
}

func TestBootstrap_CreatesRefreshHashPartialIndex(t *testing.T) {
	_, conn := openTestStore(t)

	var indexDef string
	err := conn.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'idx_sessions_refresh_hash'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected idx_sessions_refresh_hash to exist: %v", err)
	}
	if !containsAll(indexDef, "revoked_at", "rotated_at") {
		t.Errorf("expected idx_sessions_refresh_hash to be a partial index on revoked_at/rotated_at, got: %s", indexDef)
	}
}

func TestBootstrap_CreatesUserPartialIndex(t *testing.T) {
	_, conn := openTestStore(t)

	var indexDef string
	err := conn.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'idx_sessions_user'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected idx_sessions_user to exist: %v", err)
	}
	if !containsAll(indexDef, "user_id", "tenant_id", "revoked_at") {
		t.Errorf("expected idx_sessions_user to cover user_id/tenant_id, partial on revoked_at, got: %s", indexDef)
	}
}

func TestBootstrap_CreatesFamilyIndex(t *testing.T) {
	_, conn := openTestStore(t)

	var indexDef string
	err := conn.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'idx_sessions_family'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected idx_sessions_family to exist: %v", err)
	}
	if !containsAll(indexDef, "family_id") {
		t.Errorf("expected idx_sessions_family to cover family_id, got: %s", indexDef)
	}
}

// sessionFixture inserts one real session row (FK-satisfying tenant/user
// rows created alongside it) and returns its id, cleaning up all three by
// exact row id — system.tenants/system.users/system.sessions are real
// shared tables other packages' tests race against concurrently.
func sessionFixture(t *testing.T, store *Store, conn *sql.DB) (sessionID, userID string) {
	t.Helper()
	ctx := context.Background()

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}

	slug := fmt.Sprintf("sessiontest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Session Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	userID, err = userStore.FindOrCreateInvited(ctx, slug+"@example.com")
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })

	sessionID = uuid.NewString()
	row := Row{
		ID:          sessionID,
		UserID:      userID,
		TenantID:    tt.ID,
		DeviceID:    uuid.NewString(),
		RefreshHash: "fixture-hash",
		ExpiresAt:   time.Now().Add(30 * 24 * time.Hour),
	}
	if err := store.Insert(ctx, row); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE id = $1`, sessionID) })

	return sessionID, userID
}

func TestInsert_RoundTripsFields(t *testing.T) {
	store, conn := openTestStore(t)
	sessionID, userID := sessionFixture(t, store, conn)

	var gotUserID, refreshHash string
	err := conn.QueryRowContext(context.Background(),
		`SELECT user_id, refresh_hash FROM system.sessions WHERE id = $1`, sessionID,
	).Scan(&gotUserID, &refreshHash)
	if err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if gotUserID != userID {
		t.Errorf("user_id = %q, want %q", gotUserID, userID)
	}
	if refreshHash != "fixture-hash" {
		t.Errorf("refresh_hash = %q, want fixture-hash", refreshHash)
	}
}

func TestInsert_MFAFieldsRoundTripWhenSet(t *testing.T) {
	store, conn := openTestStore(t)
	fixtureSessionID, userID := sessionFixture(t, store, conn)

	var tenantID string
	if err := conn.QueryRowContext(context.Background(),
		`SELECT tenant_id FROM system.sessions WHERE id = $1`, fixtureSessionID,
	).Scan(&tenantID); err != nil {
		t.Fatalf("query fixture tenant_id: %v", err)
	}

	credID := uuid.NewString()
	verifiedAt := time.Now().Add(-time.Minute)
	mfaSessionID := uuid.NewString()
	if err := store.Insert(context.Background(), Row{
		ID:              mfaSessionID,
		UserID:          userID,
		TenantID:        tenantID,
		DeviceID:        uuid.NewString(),
		RefreshHash:     "mfa-fixture-hash",
		ExpiresAt:       time.Now().Add(30 * 24 * time.Hour),
		MFAMethod:       "totp",
		MFAVerifiedAt:   &verifiedAt,
		MFACredentialID: credID,
	}); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE id = $1`, mfaSessionID) })

	var gotMethod, gotCredID string
	var gotVerifiedAt time.Time
	err := conn.QueryRowContext(context.Background(),
		`SELECT mfa_method, mfa_verified_at, mfa_credential_id FROM system.sessions WHERE id = $1`, mfaSessionID,
	).Scan(&gotMethod, &gotVerifiedAt, &gotCredID)
	if err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if gotMethod != "totp" {
		t.Errorf("mfa_method = %q, want totp", gotMethod)
	}
	if gotVerifiedAt.Unix() != verifiedAt.Unix() {
		t.Errorf("mfa_verified_at = %v, want %v", gotVerifiedAt, verifiedAt)
	}
	if gotCredID != credID {
		t.Errorf("mfa_credential_id = %q, want %q", gotCredID, credID)
	}
}

func TestInsert_MFAFieldsAreNullWhenUnset(t *testing.T) {
	store, conn := openTestStore(t)
	sessionID, _ := sessionFixture(t, store, conn)

	var method, credID sql.NullString
	var verifiedAt sql.NullTime
	err := conn.QueryRowContext(context.Background(),
		`SELECT mfa_method, mfa_verified_at, mfa_credential_id FROM system.sessions WHERE id = $1`, sessionID,
	).Scan(&method, &verifiedAt, &credID)
	if err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if method.Valid || verifiedAt.Valid || credID.Valid {
		t.Errorf("mfa_method/mfa_verified_at/mfa_credential_id = %v/%v/%v, want all NULL", method, verifiedAt, credID)
	}
}

func TestUpdateMFAAssurance_SetsColumnsOnExistingRow(t *testing.T) {
	store, conn := openTestStore(t)
	sessionID, _ := sessionFixture(t, store, conn)

	credID := uuid.NewString()
	verifiedAt := time.Now().Add(-30 * time.Second)
	if err := store.UpdateMFAAssurance(context.Background(), sessionID, "totp", verifiedAt, credID); err != nil {
		t.Fatalf("UpdateMFAAssurance() error: %v", err)
	}

	var gotMethod, gotCredID string
	var gotVerifiedAt time.Time
	err := conn.QueryRowContext(context.Background(),
		`SELECT mfa_method, mfa_verified_at, mfa_credential_id FROM system.sessions WHERE id = $1`, sessionID,
	).Scan(&gotMethod, &gotVerifiedAt, &gotCredID)
	if err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if gotMethod != "totp" {
		t.Errorf("mfa_method = %q, want totp", gotMethod)
	}
	if gotVerifiedAt.Unix() != verifiedAt.Unix() {
		t.Errorf("mfa_verified_at = %v, want %v", gotVerifiedAt, verifiedAt)
	}
	if gotCredID != credID {
		t.Errorf("mfa_credential_id = %q, want %q", gotCredID, credID)
	}
}

func TestUpdateMFAAssurance_OverwritesPreviousValue(t *testing.T) {
	store, conn := openTestStore(t)
	sessionID, _ := sessionFixture(t, store, conn)
	ctx := context.Background()

	first := uuid.NewString()
	if err := store.UpdateMFAAssurance(ctx, sessionID, "totp", time.Now().Add(-time.Hour), first); err != nil {
		t.Fatalf("first UpdateMFAAssurance() error: %v", err)
	}

	second := uuid.NewString()
	verifiedAt := time.Now()
	if err := store.UpdateMFAAssurance(ctx, sessionID, "webauthn", verifiedAt, second); err != nil {
		t.Fatalf("second UpdateMFAAssurance() error: %v", err)
	}

	var gotMethod, gotCredID string
	var gotVerifiedAt time.Time
	err := conn.QueryRowContext(ctx,
		`SELECT mfa_method, mfa_verified_at, mfa_credential_id FROM system.sessions WHERE id = $1`, sessionID,
	).Scan(&gotMethod, &gotVerifiedAt, &gotCredID)
	if err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if gotMethod != "webauthn" {
		t.Errorf("mfa_method = %q, want webauthn (reverify's new factor)", gotMethod)
	}
	if gotCredID != second {
		t.Errorf("mfa_credential_id = %q, want %q (the second call's credential)", gotCredID, second)
	}
	if gotVerifiedAt.Unix() != verifiedAt.Unix() {
		t.Errorf("mfa_verified_at = %v, want %v", gotVerifiedAt, verifiedAt)
	}
}

func TestUpdateMFAAssurance_UnknownIDReturnsErrSessionNotFound(t *testing.T) {
	store, _ := openTestStore(t)

	err := store.UpdateMFAAssurance(context.Background(), uuid.NewString(), "totp", time.Now(), uuid.NewString())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("UpdateMFAAssurance() error = %v, want ErrSessionNotFound", err)
	}
}

func TestUpdateMFAAssurance_RevokedSessionReturnsErrSessionNotFound(t *testing.T) {
	store, conn := openTestStore(t)
	sessionID, _ := sessionFixture(t, store, conn)
	ctx := context.Background()

	if err := store.Revoke(ctx, sessionID, "test"); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	err := store.UpdateMFAAssurance(ctx, sessionID, "totp", time.Now(), uuid.NewString())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("UpdateMFAAssurance() on a revoked session error = %v, want ErrSessionNotFound", err)
	}
}

func TestRevoke_SetsRevokedAtAndReason(t *testing.T) {
	store, conn := openTestStore(t)
	sessionID, _ := sessionFixture(t, store, conn)

	if err := store.Revoke(context.Background(), sessionID, "logout"); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	var revokedAt sql.NullTime
	var reason string
	err := conn.QueryRowContext(context.Background(),
		`SELECT revoked_at, revoke_reason FROM system.sessions WHERE id = $1`, sessionID,
	).Scan(&revokedAt, &reason)
	if err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if !revokedAt.Valid {
		t.Error("revoked_at is NULL, want set")
	}
	if reason != "logout" {
		t.Errorf("revoke_reason = %q, want logout", reason)
	}
}

func TestRevoke_UnknownIDReturnsErrSessionNotFound(t *testing.T) {
	store, _ := openTestStore(t)

	err := store.Revoke(context.Background(), uuid.NewString(), "logout")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("Revoke() error = %v, want ErrSessionNotFound", err)
	}
}

func TestRevoke_IsIdempotent(t *testing.T) {
	store, conn := openTestStore(t)
	sessionID, _ := sessionFixture(t, store, conn)

	if err := store.Revoke(context.Background(), sessionID, "logout"); err != nil {
		t.Fatalf("first Revoke() error: %v", err)
	}
	if err := store.Revoke(context.Background(), sessionID, "password_change"); err != nil {
		t.Fatalf("second Revoke() error: %v", err)
	}

	var reason string
	err := conn.QueryRowContext(context.Background(),
		`SELECT revoke_reason FROM system.sessions WHERE id = $1`, sessionID,
	).Scan(&reason)
	if err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if reason != "password_change" {
		t.Errorf("revoke_reason = %q, want password_change (the second call's reason)", reason)
	}
}

func TestRevokeAllForUser_RevokesEveryNonRevokedSession(t *testing.T) {
	store, conn := openTestStore(t)
	id1, userID := sessionFixture(t, store, conn)

	// A second session for the same user, same fixture tenant/user rows
	// reused by inserting directly rather than calling sessionFixture
	// again (which would create a second, unrelated tenant/user pair).
	id2 := uuid.NewString()
	var tenantID string
	if err := conn.QueryRowContext(context.Background(), `SELECT tenant_id FROM system.sessions WHERE id = $1`, id1).Scan(&tenantID); err != nil {
		t.Fatalf("query tenant_id: %v", err)
	}
	if err := store.Insert(context.Background(), Row{
		ID: id2, UserID: userID, TenantID: tenantID, DeviceID: uuid.NewString(),
		RefreshHash: "fixture-hash-2", ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("Insert() second session: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE id = $1`, id2) })

	if err := store.RevokeAllForUser(context.Background(), userID, "admin"); err != nil {
		t.Fatalf("RevokeAllForUser() error: %v", err)
	}

	var revokedCount int
	err := conn.QueryRowContext(context.Background(),
		`SELECT count(*) FROM system.sessions WHERE user_id = $1 AND revoked_at IS NOT NULL`, userID,
	).Scan(&revokedCount)
	if err != nil {
		t.Fatalf("count revoked sessions: %v", err)
	}
	if revokedCount != 2 {
		t.Errorf("revoked count = %d, want 2", revokedCount)
	}
}

func TestNonRevokedIDsForUser_ExcludesRevoked(t *testing.T) {
	store, conn := openTestStore(t)
	sessionID, userID := sessionFixture(t, store, conn)

	ids, err := store.NonRevokedIDsForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("NonRevokedIDsForUser() error: %v", err)
	}
	if len(ids) != 1 || ids[0] != sessionID {
		t.Errorf("ids = %v, want [%s]", ids, sessionID)
	}

	if err := store.Revoke(context.Background(), sessionID, "logout"); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	ids, err = store.NonRevokedIDsForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("second NonRevokedIDsForUser() error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ids = %v, want empty after revocation", ids)
	}
}

func TestRevokeAllForTenant_RevokesEveryNonRevokedSession(t *testing.T) {
	store, conn := openTestStore(t)
	id1, userID := sessionFixture(t, store, conn)

	var tenantID string
	if err := conn.QueryRowContext(context.Background(), `SELECT tenant_id FROM system.sessions WHERE id = $1`, id1).Scan(&tenantID); err != nil {
		t.Fatalf("query tenant_id: %v", err)
	}

	// A second user's session in the same tenant — a tenant-scoped revoke
	// must reach across users, unlike RevokeAllForUser.
	id2 := uuid.NewString()
	if err := store.Insert(context.Background(), Row{
		ID: id2, UserID: userID, TenantID: tenantID, DeviceID: uuid.NewString(),
		RefreshHash: "fixture-hash-2", ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("Insert() second session: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE id = $1`, id2) })

	if err := store.RevokeAllForTenant(context.Background(), tenantID, "tenant_suspended"); err != nil {
		t.Fatalf("RevokeAllForTenant() error: %v", err)
	}

	var revokedCount int
	err := conn.QueryRowContext(context.Background(),
		`SELECT count(*) FROM system.sessions WHERE tenant_id = $1 AND revoked_at IS NOT NULL`, tenantID,
	).Scan(&revokedCount)
	if err != nil {
		t.Fatalf("count revoked sessions: %v", err)
	}
	if revokedCount != 2 {
		t.Errorf("revoked count = %d, want 2", revokedCount)
	}
}

func TestNonRevokedIDsForTenant_ExcludesRevokedAndOtherTenants(t *testing.T) {
	store, conn := openTestStore(t)
	sessionID, _ := sessionFixture(t, store, conn)
	otherSessionID, _ := sessionFixture(t, store, conn)

	var tenantID string
	if err := conn.QueryRowContext(context.Background(), `SELECT tenant_id FROM system.sessions WHERE id = $1`, sessionID).Scan(&tenantID); err != nil {
		t.Fatalf("query tenant_id: %v", err)
	}

	ids, err := store.NonRevokedIDsForTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("NonRevokedIDsForTenant() error: %v", err)
	}
	if len(ids) != 1 || ids[0] != sessionID {
		t.Errorf("ids = %v, want [%s] (not %s, a different tenant's session)", ids, sessionID, otherSessionID)
	}

	if err := store.Revoke(context.Background(), sessionID, "logout"); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	ids, err = store.NonRevokedIDsForTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("second NonRevokedIDsForTenant() error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ids = %v, want empty after revocation", ids)
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
