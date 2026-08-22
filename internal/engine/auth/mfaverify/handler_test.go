package mfaverify

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pquernatotp "github.com/pquerna/otp/totp"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
const testOrigin = "https://acmecorp.goerp.io"

type fixture struct {
	handler    *Handler
	mfaTokens  *mfatoken.Codec
	rowKeys    *rowcrypt.RowKeySet
	tenantID   string
	tenantSlug string
	userID     string
	conn       *sql.DB
	cache      *cache.Client
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockSharedKeyTables(t, conn)

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}
	sessionStore := session.NewStore(conn)
	if err := sessionStore.Bootstrap(ctx); err != nil {
		t.Fatalf("session Bootstrap() error: %v", err)
	}
	mfaStore := mfa.NewStore(conn)
	if err := mfaStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfa Bootstrap() error: %v", err)
	}
	roleStore := role.NewStore(conn)

	signingKeyStore := signingkey.NewStore(conn, &secrets.EnvBackend{})
	if err := signingKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("signingkey Bootstrap() error: %v", err)
	}
	signingKeySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("signingkey LoadOrGenerate() error: %v", err)
	}

	mfaTokenKeyStore := mfatoken.NewStore(conn, &secrets.EnvBackend{})
	if err := mfaTokenKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfatoken Bootstrap() error: %v", err)
	}
	mfaTokenKeySet, err := mfaTokenKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("mfatoken LoadOrGenerate() error: %v", err)
	}
	mfaTokens := mfatoken.NewCodec(&mfaTokenKeySet.Active)

	// EnvBackend (ephemeral, never persists) — same reasoning as
	// signingKeyStore/mfaTokenKeyStore above: LoadOrGenerate must produce
	// a fresh, usable key every test run regardless of what a previous
	// test in this package left in system.row_encryption_keys, since a
	// row without recoverable key material (e.g. from another test's own
	// in-process memoryBackend) would otherwise break decryption here.
	rowCryptStore := rowcrypt.NewStore(conn, &secrets.EnvBackend{})
	if err := rowCryptStore.Bootstrap(ctx); err != nil {
		t.Fatalf("rowcrypt Bootstrap() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.row_encryption_keys`) })
	rowKeys, err := rowCryptStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("rowcrypt LoadOrGenerate() error: %v", err)
	}

	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	issuer := authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore)
	totpService := totp.NewService(mfaStore, rowKeys, cacheClient)
	recoveryService := recoverycode.NewService(mfaStore)
	handler := NewHandler(mfaTokens, cacheClient, totpService, recoveryService, tenantStore, issuer)

	slug := fmt.Sprintf("mfaverifytest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "MFA Verify Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	email := slug + "@example.com"
	userID, err := userStore.FindOrCreateInvited(ctx, email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })
	if _, err := conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, userID); err != nil {
		t.Fatalf("activate fixture user: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.user_mfa WHERE user_id = $1`, userID) })
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })

	schema := tenantschema.Name(slug)
	if _, err := conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)) })
	if err := roleStore.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := roleStore.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := roleStore.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	if _, err := conn.Exec(fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), userID, roleID); err != nil {
		t.Fatalf("grant admin role: %v", err)
	}

	return &fixture{
		handler:    handler,
		mfaTokens:  mfaTokens,
		rowKeys:    rowKeys,
		tenantID:   tt.ID,
		tenantSlug: slug,
		userID:     userID,
		conn:       conn,
		cache:      cacheClient,
	}
}

// lockSharedKeyTables mirrors loginflow/totp's own lock helpers —
// serializes this package's tests against every other package's test
// touching the same shared signing-key/row-encryption-key/mfa-token-key
// tables.
func lockSharedKeyTables(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, name := range []string{
		"test.jwt_signing_keys_table",
		"test.row_encryption_keys_table",
		"test.mfa_token_signing_keys_table",
	} {
		key := db.AdvisoryLockKey(name)
		conn, err := pool.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire dedicated connection for %s lock: %v", name, err)
		}
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
			t.Fatalf("acquire %s advisory lock: %v", name, err)
		}
		t.Cleanup(func() {
			_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
			_ = conn.Close()
		})
	}
}

func (f *fixture) enrollTOTP(t *testing.T) (code string) {
	t.Helper()
	svc := totp.NewService(mfa.NewStore(f.conn), f.rowKeys, f.cache)
	_, cred, err := svc.Enroll(context.Background(), f.userID, "user@example.com", nil)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}
	var ciphertext []byte
	if err := f.conn.QueryRowContext(context.Background(),
		"SELECT credential FROM system.user_mfa WHERE id = $1", cred.ID,
	).Scan(&ciphertext); err != nil {
		t.Fatalf("query stored credential: %v", err)
	}
	secret, err := f.rowKeys.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("decrypt stored credential: %v", err)
	}
	code, err = pquernatotp.GenerateCode(string(secret), time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}
	return code
}

func (f *fixture) issueMFAToken(t *testing.T, origin string) (token, txn string) {
	t.Helper()
	token, txn, err := f.mfaTokens.Issue(f.userID, f.tenantID, origin)
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return token, txn
}

func (f *fixture) doVerify(t *testing.T, body map[string]any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/mfa/verify", bytes.NewReader(b))
	req.RemoteAddr = "203.0.113.7:54321"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestServeHTTP_ValidTOTPCodeIssuesSession(t *testing.T) {
	f := newFixture(t)
	code := f.enrollTOTP(t)
	token, _ := f.issueMFAToken(t, testOrigin)

	rec := f.doVerify(t, map[string]any{
		"mfa_token": token,
		"type":      "totp",
		"code":      code,
		"device_id": "",
	}, map[string]string{
		"Origin":        testOrigin,
		"X-Client-Type": "cli",
		"Content-Type":  "application/json",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["access_token"] == "" || resp["access_token"] == nil {
		t.Error("access_token missing or empty")
	}
	if resp["refresh_token"] == "" || resp["refresh_token"] == nil {
		t.Error("refresh_token missing or empty")
	}

	var mfaMethod string
	var mfaVerifiedAt sql.NullTime
	var mfaCredentialID sql.NullString
	if err := f.conn.QueryRowContext(context.Background(),
		`SELECT mfa_method, mfa_verified_at, mfa_credential_id FROM system.sessions WHERE user_id = $1`, f.userID,
	).Scan(&mfaMethod, &mfaVerifiedAt, &mfaCredentialID); err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if mfaMethod != "totp" {
		t.Errorf("sessions.mfa_method = %q, want %q", mfaMethod, "totp")
	}
	if !mfaVerifiedAt.Valid {
		t.Error("sessions.mfa_verified_at is NULL, want set")
	}
	if !mfaCredentialID.Valid || mfaCredentialID.String == "" {
		t.Error("sessions.mfa_credential_id is NULL/empty, want the matched credential's id")
	}
}

func TestServeHTTP_ValidRecoveryCodeConsumesItAndIssuesSession(t *testing.T) {
	f := newFixture(t)
	svc := recoverycode.NewService(mfa.NewStore(f.conn))
	codes, err := svc.Enroll(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}
	token, _ := f.issueMFAToken(t, testOrigin)

	rec := f.doVerify(t, map[string]any{
		"mfa_token": token,
		"type":      "recovery_code",
		"code":      codes[0],
	}, map[string]string{"Origin": testOrigin, "X-Client-Type": "cli"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	// A second attempt with the same recovery code must fail — it was
	// consumed by the first successful verification.
	token2, _ := f.issueMFAToken(t, testOrigin)
	rec2 := f.doVerify(t, map[string]any{
		"mfa_token": token2,
		"type":      "recovery_code",
		"code":      codes[0],
	}, map[string]string{"Origin": testOrigin, "X-Client-Type": "cli"})
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("second use status = %d, want 401 (code already consumed)", rec2.Code)
	}
}

func TestServeHTTP_WrongCodeRejected(t *testing.T) {
	f := newFixture(t)
	f.enrollTOTP(t)
	token, _ := f.issueMFAToken(t, testOrigin)

	rec := f.doVerify(t, map[string]any{
		"mfa_token": token,
		"type":      "totp",
		"code":      "000000",
	}, map[string]string{"Origin": testOrigin, "X-Client-Type": "cli"})

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_TokenIsSingleUse(t *testing.T) {
	f := newFixture(t)
	code := f.enrollTOTP(t)
	token, _ := f.issueMFAToken(t, testOrigin)

	first := f.doVerify(t, map[string]any{
		"mfa_token": token, "type": "totp", "code": code,
	}, map[string]string{"Origin": testOrigin, "X-Client-Type": "cli"})
	if first.Code != http.StatusOK {
		t.Fatalf("first attempt status = %d, want 200; body = %s", first.Code, first.Body.String())
	}

	second := f.doVerify(t, map[string]any{
		"mfa_token": token, "type": "totp", "code": code,
	}, map[string]string{"Origin": testOrigin, "X-Client-Type": "cli"})
	if second.Code != http.StatusUnauthorized {
		t.Errorf("replayed mfa_token status = %d, want 401", second.Code)
	}
}

func TestServeHTTP_OriginMismatchRejected(t *testing.T) {
	f := newFixture(t)
	code := f.enrollTOTP(t)
	token, _ := f.issueMFAToken(t, testOrigin)

	rec := f.doVerify(t, map[string]any{
		"mfa_token": token, "type": "totp", "code": code,
	}, map[string]string{"Origin": "https://evil.example.com", "X-Client-Type": "cli"})

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_LocksOutAfterFiveFailures(t *testing.T) {
	f := newFixture(t)
	f.enrollTOTP(t)

	for attempt := range maxAttempts {
		token, _ := f.issueMFAToken(t, testOrigin)
		rec := f.doVerify(t, map[string]any{
			"mfa_token": token, "type": "totp", "code": "000000",
		}, map[string]string{"Origin": testOrigin, "X-Client-Type": "cli"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt+1, rec.Code)
		}
	}

	// The 6th attempt, even with the correct code, must be locked out.
	code := f.enrollTOTP(t)
	token, _ := f.issueMFAToken(t, testOrigin)
	rec := f.doVerify(t, map[string]any{
		"mfa_token": token, "type": "totp", "code": code,
	}, map[string]string{"Origin": testOrigin, "X-Client-Type": "cli"})
	if rec.Code != http.StatusLocked {
		t.Errorf("status after %d failures = %d, want 423", maxAttempts, rec.Code)
	}
}

func TestServeHTTP_MalformedMFATokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doVerify(t, map[string]any{
		"mfa_token": "not-a-real-token", "type": "totp", "code": "123456",
	}, map[string]string{"Origin": testOrigin})

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}
