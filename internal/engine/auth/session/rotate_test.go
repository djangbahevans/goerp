package session

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/google/uuid"
)

// rotateFixture is one login's worth of FK-satisfying rows plus a first
// session row with a caller-controlled hash/device_id, so a test can
// present that exact hash back to Rotate.
type rotateFixture struct {
	store       *Store
	conn        *sql.DB
	tenantID    string
	userID      string
	firstID     string
	familyID    string
	deviceID    string
	refreshHash string
	otherHash   string // a distinct hash never inserted anywhere
}

func newRotateFixture(t *testing.T) *rotateFixture {
	t.Helper()
	store, conn := openTestStore(t)
	ctx := context.Background()

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}

	slug := fmt.Sprintf("rotatetest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Rotate Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	userID, err := userStore.FindOrCreateInvited(ctx, slug+"@example.com")
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })

	firstID := uuid.NewString()
	deviceID := uuid.NewString()
	refreshHash := "hash-" + uuid.NewString()
	if err := store.Insert(ctx, Row{
		ID:          firstID,
		UserID:      userID,
		TenantID:    tt.ID,
		DeviceID:    deviceID,
		RefreshHash: refreshHash,
		ExpiresAt:   time.Now().Add(30 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE family_id = $1`, firstID) })

	return &rotateFixture{
		store:       store,
		conn:        conn,
		tenantID:    tt.ID,
		userID:      userID,
		firstID:     firstID,
		familyID:    firstID, // Insert's own convention: id == family_id for a fresh login
		deviceID:    deviceID,
		refreshHash: refreshHash,
		otherHash:   "hash-" + uuid.NewString(),
	}
}

func (f *rotateFixture) rotate(t *testing.T, presentedHash, requestDeviceID string) RotateResult {
	t.Helper()
	result, err := f.store.Rotate(context.Background(), presentedHash, uuid.NewString(), "hash-"+uuid.NewString(), requestDeviceID, time.Now().Add(30*24*time.Hour), "", "", "")
	if err != nil {
		t.Fatalf("Rotate() error: %v", err)
	}
	return result
}

func TestRotate_NotFoundForUnknownHash(t *testing.T) {
	f := newRotateFixture(t)

	result := f.rotate(t, f.otherHash, f.deviceID)

	if result.Outcome != RotateNotFound {
		t.Errorf("Outcome = %v, want RotateNotFound", result.Outcome)
	}
}

func TestRotate_LiveTokenRotatesSuccessfully(t *testing.T) {
	f := newRotateFixture(t)

	result := f.rotate(t, f.refreshHash, f.deviceID)

	if result.Outcome != RotateOK {
		t.Fatalf("Outcome = %v, want RotateOK", result.Outcome)
	}
	if result.UserID != f.userID {
		t.Errorf("UserID = %q, want %q", result.UserID, f.userID)
	}
	if result.TenantID != f.tenantID {
		t.Errorf("TenantID = %q, want %q", result.TenantID, f.tenantID)
	}

	var rotatedAt sql.NullTime
	if err := f.conn.QueryRowContext(context.Background(),
		`SELECT rotated_at FROM system.sessions WHERE id = $1`, f.firstID,
	).Scan(&rotatedAt); err != nil {
		t.Fatalf("query old row: %v", err)
	}
	if !rotatedAt.Valid {
		t.Error("old row's rotated_at is NULL, want set")
	}

	var newRowCount int
	if err := f.conn.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM system.sessions WHERE family_id = $1 AND rotated_at IS NULL AND revoked_at IS NULL`, f.familyID,
	).Scan(&newRowCount); err != nil {
		t.Fatalf("count live rows: %v", err)
	}
	if newRowCount != 1 {
		t.Errorf("live rows in family = %d, want exactly 1", newRowCount)
	}
}

func TestRotate_CarriesForwardUserAgentIPAndCountryOnTheNewRow(t *testing.T) {
	f := newRotateFixture(t)
	newSessionID := uuid.NewString()

	result, err := f.store.Rotate(context.Background(), f.refreshHash, newSessionID, "hash-"+uuid.NewString(), f.deviceID, time.Now().Add(30*24*time.Hour), "Mozilla/5.0 test-agent", "203.0.113.7", "GH")
	if err != nil {
		t.Fatalf("Rotate() error: %v", err)
	}
	if result.Outcome != RotateOK {
		t.Fatalf("Outcome = %v, want RotateOK", result.Outcome)
	}

	var userAgent, countryCode sql.NullString
	var ipAddress sql.NullString
	if err := f.conn.QueryRowContext(context.Background(),
		`SELECT user_agent, host(ip_address), country_code FROM system.sessions WHERE id = $1`, newSessionID,
	).Scan(&userAgent, &ipAddress, &countryCode); err != nil {
		t.Fatalf("query new row: %v", err)
	}
	if userAgent.String != "Mozilla/5.0 test-agent" {
		t.Errorf("user_agent = %q, want %q", userAgent.String, "Mozilla/5.0 test-agent")
	}
	if ipAddress.String != "203.0.113.7" {
		t.Errorf("ip_address = %q, want %q", ipAddress.String, "203.0.113.7")
	}
	if countryCode.String != "GH" {
		t.Errorf("country_code = %q, want %q", countryCode.String, "GH")
	}
}

func TestRotate_ReusingRotatedTokenFromSameDeviceDoesNotRevoke(t *testing.T) {
	f := newRotateFixture(t)
	first := f.rotate(t, f.refreshHash, f.deviceID)
	if first.Outcome != RotateOK {
		t.Fatalf("first rotate Outcome = %v, want RotateOK", first.Outcome)
	}

	replay := f.rotate(t, f.refreshHash, f.deviceID)

	if replay.Outcome != RotateReplaySameDevice {
		t.Fatalf("Outcome = %v, want RotateReplaySameDevice", replay.Outcome)
	}
	var revokedCount int
	if err := f.conn.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM system.sessions WHERE family_id = $1 AND revoked_at IS NOT NULL`, f.familyID,
	).Scan(&revokedCount); err != nil {
		t.Fatalf("count revoked rows: %v", err)
	}
	if revokedCount != 0 {
		t.Errorf("revoked rows = %d, want 0 (same-device double-submit must not revoke)", revokedCount)
	}
}

func TestRotate_ReusingRotatedTokenFromDifferentDeviceRevokesFamily(t *testing.T) {
	f := newRotateFixture(t)
	legitimateNewHash := "hash-" + uuid.NewString()
	first, err := f.store.Rotate(context.Background(), f.refreshHash, uuid.NewString(), legitimateNewHash, f.deviceID, time.Now().Add(30*24*time.Hour), "", "", "")
	if err != nil {
		t.Fatalf("first Rotate() error: %v", err)
	}
	if first.Outcome != RotateOK {
		t.Fatalf("first rotate Outcome = %v, want RotateOK", first.Outcome)
	}

	attackerDeviceID := uuid.NewString()
	replay := f.rotate(t, f.refreshHash, attackerDeviceID)

	if replay.Outcome != RotateReplayDifferentDevice {
		t.Fatalf("Outcome = %v, want RotateReplayDifferentDevice", replay.Outcome)
	}
	var nonRevokedCount int
	if err := f.conn.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM system.sessions WHERE family_id = $1 AND revoked_at IS NULL`, f.familyID,
	).Scan(&nonRevokedCount); err != nil {
		t.Fatalf("count non-revoked rows: %v", err)
	}
	if nonRevokedCount != 0 {
		t.Errorf("non-revoked rows = %d, want 0 (cross-device replay must revoke the entire family)", nonRevokedCount)
	}

	// The legitimate owner's own subsequent use of the token rotate()
	// actually minted for them now finds its family already revoked —
	// they're locked out too, the whole point of family-wide revocation
	// on detected compromise.
	third := f.rotate(t, legitimateNewHash, f.deviceID)
	if third.Outcome != RotateFamilyRevoked {
		t.Fatalf("third rotate Outcome = %v, want RotateFamilyRevoked", third.Outcome)
	}
}

func TestRotate_AlreadyRevokedFamilyRejected(t *testing.T) {
	f := newRotateFixture(t)
	if _, err := f.conn.Exec(`UPDATE system.sessions SET revoked_at = NOW(), revoke_reason = 'logout' WHERE id = $1`, f.firstID); err != nil {
		t.Fatalf("revoke fixture row: %v", err)
	}

	result := f.rotate(t, f.refreshHash, f.deviceID)

	if result.Outcome != RotateFamilyRevoked {
		t.Errorf("Outcome = %v, want RotateFamilyRevoked", result.Outcome)
	}
}

// TestRotate_ConcurrentRequestsForSameTokenDoNotRace guards the exact
// property auth-internals.md §4 documents FOR UPDATE for: two requests
// presenting the identical live token must not both succeed, and the
// loser must see genuinely post-commit state (the replay branch), not a
// stale pre-commit snapshot racing an update of its own.
func TestRotate_ConcurrentRequestsForSameTokenDoNotRace(t *testing.T) {
	f := newRotateFixture(t)

	var wg sync.WaitGroup
	results := make([]RotateResult, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := f.store.Rotate(context.Background(), f.refreshHash, uuid.NewString(), "hash-"+uuid.NewString(), f.deviceID, time.Now().Add(30*24*time.Hour), "", "", "")
			if err != nil {
				t.Errorf("concurrent Rotate() error: %v", err)
				return
			}
			results[i] = result
		}(i)
	}
	wg.Wait()

	okCount, replayCount := 0, 0
	for _, r := range results {
		switch r.Outcome {
		case RotateOK:
			okCount++
		case RotateReplaySameDevice:
			replayCount++
		default:
			t.Errorf("unexpected outcome %v among concurrent results", r.Outcome)
		}
	}
	if okCount != 1 {
		t.Errorf("RotateOK count = %d, want exactly 1", okCount)
	}
	if replayCount != 1 {
		t.Errorf("RotateReplaySameDevice count = %d, want exactly 1 (the loser, same device, must not be treated as compromise)", replayCount)
	}

	var liveCount int
	if err := f.conn.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM system.sessions WHERE family_id = $1 AND rotated_at IS NULL AND revoked_at IS NULL`, f.familyID,
	).Scan(&liveCount); err != nil {
		t.Fatalf("count live rows: %v", err)
	}
	if liveCount != 1 {
		t.Errorf("live rows in family = %d, want exactly 1 — a race would leave 0 or 2", liveCount)
	}
}
