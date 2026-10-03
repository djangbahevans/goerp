package mfa

import (
	"context"
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/session"
)

func TestMFAChange_RevokesConcurrentSessionRotation(t *testing.T) {
	e := openTestEnv(t)
	userID := e.createUser(t)
	sessions := session.NewStore(e.conn)
	if err := sessions.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	originalID := uuid.NewV7().String()
	deviceID := uuid.NewV7().String()
	if err := sessions.Insert(ctx, session.Row{
		ID:          originalID,
		UserID:      userID,
		TenantID:    e.tenant.ID,
		DeviceID:    deviceID,
		RefreshHash: originalID,
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	rotationLocked := make(chan struct{})
	resumeRotation := make(chan struct{})
	rotationDone := make(chan error, 1)
	successorID := uuid.NewV7().String()
	go func() {
		result, err := sessions.Rotate(ctx, originalID, successorID, successorID, deviceID, time.Now(),
			func(string, bool, time.Time) (time.Time, error) {
				close(rotationLocked)
				select {
				case <-resumeRotation:
					return time.Now().Add(time.Hour), nil
				case <-ctx.Done():
					return time.Time{}, ctx.Err()
				}
			}, "", "", "")
		if err == nil && result.Outcome != session.RotateOK {
			err = fmt.Errorf("rotation outcome = %v, want RotateOK", result.Outcome)
		}
		rotationDone <- err
	}()

	select {
	case <-rotationLocked:
	case <-ctx.Done():
		t.Fatal("rotation did not acquire the session lock")
	}

	tx, err := e.conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := e.store.LockUserTx(ctx, tx, userID); err != nil {
		t.Fatal(err)
	}

	revocationDone := make(chan error, 1)
	go func() {
		_, err := sessions.RevokeAllForUserInTenantTx(ctx, tx, userID, e.tenant.ID, "mfa_admin_reset")
		revocationDone <- err
	}()
	close(resumeRotation)

	if err := <-rotationDone; err != nil {
		t.Fatalf("concurrent rotation: %v", err)
	}

	if err := <-revocationDone; err != nil {
		t.Fatalf("concurrent MFA revocation: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	remaining, err := sessions.NonRevokedIDsForUserInTenant(ctx, userID, e.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(remaining) != 0 {
		t.Fatalf("sessions survived MFA revocation: %v", remaining)
	}
}
