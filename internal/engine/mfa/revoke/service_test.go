package revoke

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// fixture is a Service plus one real user with an enrolled MFA factor and
// an active session (and its FK-satisfying tenant row) — mirrors
// sessionrevoke's own fixture convention.
type fixture struct {
	service   *Service
	sessions  *session.Store
	mfaStore  *mfa.Store
	sessionID string
	userID    string
	conn      *sql.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	sessionStore := session.NewStore(conn)
	if err := sessionStore.Bootstrap(ctx); err != nil {
		t.Fatalf("session Bootstrap() error: %v", err)
	}
	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}
	mfaStore := mfa.NewStore(conn)
	if err := mfaStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfa Bootstrap() error: %v", err)
	}

	slug := fmt.Sprintf("revoketest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Revoke Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	userID, err := userStore.FindOrCreateInvited(ctx, slug+"@example.com")
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })

	sessionID := uuid.New().String()
	if err := sessionStore.Insert(ctx, session.Row{
		ID: sessionID, UserID: userID, TenantID: tt.ID, DeviceID: uuid.New().String(),
		RefreshHash: "fixture-hash", ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE id = $1`, sessionID) })

	revoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)

	return &fixture{
		service:   NewService(mfaStore, revoker),
		sessions:  sessionStore,
		mfaStore:  mfaStore,
		sessionID: sessionID,
		userID:    userID,
		conn:      conn,
	}
}

func (f *fixture) sessionIsRevoked(t *testing.T) bool {
	t.Helper()
	var revokedAt sql.NullTime
	if err := f.conn.QueryRowContext(context.Background(),
		"SELECT revoked_at FROM system.sessions WHERE id = $1", f.sessionID,
	).Scan(&revokedAt); err != nil {
		t.Fatalf("query session: %v", err)
	}
	return revokedAt.Valid
}

func (f *fixture) isRevoked(t *testing.T, credID string) bool {
	t.Helper()
	var revokedAt sql.NullTime
	if err := f.conn.QueryRowContext(t.Context(),
		"SELECT revoked_at FROM system.user_mfa WHERE id = $1", credID,
	).Scan(&revokedAt); err != nil {
		t.Fatalf("query mfa credential: %v", err)
	}
	return revokedAt.Valid
}

func (f *fixture) insert(t *testing.T, credType mfa.CredentialType) *mfa.Credential {
	t.Helper()
	cred, err := f.mfaStore.Insert(t.Context(), f.userID, credType, []byte("x"), nil)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	return cred
}

func TestRevokeFactor_RevokesFactorAndAllUserSessions(t *testing.T) {
	f := newFixture(t)
	cred := f.insert(t, mfa.CredentialTOTP)

	if err := f.service.RevokeFactor(t.Context(), f.userID, cred.ID, false); err != nil {
		t.Fatalf("RevokeFactor() error: %v", err)
	}

	if !f.isRevoked(t, cred.ID) {
		t.Error("user_mfa.revoked_at is NULL, want set")
	}
	if !f.sessionIsRevoked(t) {
		t.Error("session was not revoked after RevokeFactor(), want revoked")
	}
}

func TestRevokeFactor_LastFactorAlsoRevokesRecoveryCodes(t *testing.T) {
	f := newFixture(t)
	cred := f.insert(t, mfa.CredentialTOTP)
	code := f.insert(t, mfa.CredentialRecoveryCode)

	if err := f.service.RevokeFactor(t.Context(), f.userID, cred.ID, false); err != nil {
		t.Fatalf("RevokeFactor() error: %v", err)
	}
	if !f.isRevoked(t, code.ID) {
		t.Error("recovery code still active after removing the last factor, want revoked")
	}
}

func TestRevokeFactor_OtherFactorRemainingKeepsRecoveryCodes(t *testing.T) {
	f := newFixture(t)
	cred := f.insert(t, mfa.CredentialTOTP)
	f.insert(t, mfa.CredentialWebAuthn)
	code := f.insert(t, mfa.CredentialRecoveryCode)

	if err := f.service.RevokeFactor(t.Context(), f.userID, cred.ID, true); err != nil {
		t.Fatalf("RevokeFactor() error: %v", err)
	}
	if f.isRevoked(t, code.ID) {
		t.Error("recovery code revoked while another factor remains, want active")
	}
}

func TestRevokeFactor_LastFactorUnderPolicyChangesNothing(t *testing.T) {
	f := newFixture(t)
	cred := f.insert(t, mfa.CredentialTOTP)
	code := f.insert(t, mfa.CredentialRecoveryCode)

	err := f.service.RevokeFactor(t.Context(), f.userID, cred.ID, true)
	if !errors.Is(err, ErrRequiredByPolicy) {
		t.Fatalf("RevokeFactor() error = %v, want ErrRequiredByPolicy", err)
	}
	if f.isRevoked(t, cred.ID) || f.isRevoked(t, code.ID) {
		t.Error("a credential was revoked despite ErrRequiredByPolicy, want untouched")
	}
	if f.sessionIsRevoked(t) {
		t.Error("session was revoked despite ErrRequiredByPolicy, want untouched")
	}
}

func TestRevokeFactor_RecoveryCodeIDReturnsErrCredentialNotFound(t *testing.T) {
	f := newFixture(t)
	f.insert(t, mfa.CredentialTOTP)
	code := f.insert(t, mfa.CredentialRecoveryCode)

	err := f.service.RevokeFactor(t.Context(), f.userID, code.ID, false)
	if !errors.Is(err, mfa.ErrCredentialNotFound) {
		t.Errorf("RevokeFactor() error = %v, want ErrCredentialNotFound", err)
	}
	if f.isRevoked(t, code.ID) {
		t.Error("recovery code revoked through RevokeFactor(), want untouched")
	}
}

func TestRevokeFactor_UnknownCredentialIDReturnsErrCredentialNotFoundAndDoesNotTouchSessions(t *testing.T) {
	f := newFixture(t)

	err := f.service.RevokeFactor(context.Background(), f.userID, "00000000-0000-0000-0000-000000000000", false)
	if !errors.Is(err, mfa.ErrCredentialNotFound) {
		t.Errorf("RevokeFactor() error = %v, want ErrCredentialNotFound", err)
	}
	if f.sessionIsRevoked(t) {
		t.Error("session was revoked despite RevokeFactor() failing, want untouched")
	}
}

func TestRevokeFactor_CredentialBelongingToAnotherUserReturnsErrCredentialNotFoundAndDoesNotTouchSessions(t *testing.T) {
	f := newFixture(t)

	otherEmail := fmt.Sprintf("otheruser%d@example.com", time.Now().UnixNano())
	otherUserID, err := user.NewStore(f.conn).FindOrCreateInvited(context.Background(), otherEmail)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec("DELETE FROM system.users WHERE id = $1", otherUserID) })

	otherCred, err := f.mfaStore.Insert(context.Background(), otherUserID, mfa.CredentialTOTP, []byte("x"), nil)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}

	// f.userID (the fixture's session owner) tries to revoke a credential
	// that actually belongs to otherUserID.
	err = f.service.RevokeFactor(context.Background(), f.userID, otherCred.ID, false)
	if !errors.Is(err, mfa.ErrCredentialNotFound) {
		t.Errorf("RevokeFactor() error = %v, want ErrCredentialNotFound", err)
	}
	if f.sessionIsRevoked(t) {
		t.Error("session was revoked despite the credential not belonging to this user, want untouched")
	}

	var otherCredRevoked sql.NullTime
	if err := f.conn.QueryRowContext(context.Background(),
		"SELECT revoked_at FROM system.user_mfa WHERE id = $1", otherCred.ID,
	).Scan(&otherCredRevoked); err != nil {
		t.Fatalf("query other user's credential: %v", err)
	}
	if otherCredRevoked.Valid {
		t.Error("the other user's credential was revoked by a mismatched-owner call, want untouched")
	}
}

func TestEnroll_DoesNotRevokeSessions(t *testing.T) {
	f := newFixture(t)

	if _, err := f.mfaStore.Insert(context.Background(), f.userID, mfa.CredentialTOTP, []byte("x"), nil); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}

	if f.sessionIsRevoked(t) {
		t.Error("session was revoked merely by enrolling a new factor, want untouched")
	}
}
