package session

import (
	"context"
	"testing"
	"time"
	"uuid"
)

// TestRevocations_CatchARotationCommittedMidRevoke holds a live row's lock
// the way Rotate does, starts a revocation that blocks on it, then marks
// the row rotated and inserts its successor before committing. The
// revoking UPDATE's snapshot can't see the successor, so each revocation
// must pass again to end the session.
func TestRevocations_CatchARotationCommittedMidRevoke(t *testing.T) {
	cases := map[string]func(f *rotateFixture, keepID string) error{
		"RevokeFamily": func(f *rotateFixture, _ string) error {
			_, err := f.store.RevokeFamily(context.Background(), f.familyID, "logout")
			return err
		},
		"RevokeOtherFamiliesForUserInTenant": func(f *rotateFixture, keepID string) error {
			families, err := f.store.RevokeOtherFamiliesForUserInTenant(context.Background(), f.userID, f.tenantID, keepID, "logout")
			if err == nil && (len(families) != 1 || families[0].LiveRowID == "") {
				t.Errorf("families = %+v, want the rotated family with its successor as the live row", families)
			}
			return err
		},
		"RevokeOthersForUser": func(f *rotateFixture, keepID string) error {
			_, err := f.store.RevokeOthersForUser(context.Background(), f.userID, keepID, "password_change")
			return err
		},
	}
	for name, revoke := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRotateFixture(t)
			ctx := t.Context()
			keepID := uuid.New().String()
			if err := f.store.Insert(ctx, Row{ID: keepID, UserID: f.userID, TenantID: f.tenantID, DeviceID: uuid.New().String(), RefreshHash: "hash-" + keepID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatalf("insert kept session: %v", err)
			}
			t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.sessions WHERE family_id = $1`, keepID) })

			tx, err := f.conn.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin rotation: %v", err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.ExecContext(ctx, `UPDATE system.sessions SET rotated_at = NOW() WHERE id = $1`, f.firstID); err != nil {
				t.Fatalf("mark rotated: %v", err)
			}
			successor := uuid.New().String()
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO system.sessions (id, user_id, tenant_id, family_id, device_id, refresh_hash, expires_at)
				VALUES ($1, $2, $3, $4, $5, $6, NOW() + INTERVAL '1 day')
			`, successor, f.userID, f.tenantID, f.familyID, f.deviceID, "hash-"+successor); err != nil {
				t.Fatalf("insert successor: %v", err)
			}
			var pid int
			if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatalf("read rotation backend pid: %v", err)
			}

			done := make(chan error, 1)
			go func() { done <- revoke(f, keepID) }()
			waitForLockWaiter(t, f, pid, done)
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit rotation: %v", err)
			}
			if err := <-done; err != nil {
				t.Fatalf("revoke: %v", err)
			}

			var revoked bool
			if err := f.conn.QueryRow(`SELECT revoked_at IS NOT NULL FROM system.sessions WHERE id = $1`, successor).Scan(&revoked); err != nil {
				t.Fatalf("read successor: %v", err)
			}
			if !revoked {
				t.Error("the successor a concurrent rotation committed is still live")
			}
			if err := f.conn.QueryRow(`SELECT revoked_at IS NOT NULL FROM system.sessions WHERE id = $1`, keepID).Scan(&revoked); err != nil {
				t.Fatalf("read kept session: %v", err)
			}
			if revoked && name != "RevokeFamily" {
				t.Error("the kept session was revoked")
			}
		})
	}
}

// waitForLockWaiter blocks until some backend is blocked by backend pid,
// failing if the revocation finishes first.
func waitForLockWaiter(t *testing.T, f *rotateFixture, pid int, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("the revocation finished before blocking on the rotation's row lock: %v", err)
		default:
		}
		var waiting bool
		if err := f.conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
			t.Fatalf("read blocked backends: %v", err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the revocation never blocked on the rotation's row lock")
}
