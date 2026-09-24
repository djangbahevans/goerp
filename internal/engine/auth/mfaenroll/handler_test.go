package mfaenroll

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	pquernatotp "github.com/pquerna/otp/totp"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

type recordingAudit struct {
	mu   sync.Mutex
	rows []authaudit.Row
}

func (a *recordingAudit) Insert(_ context.Context, row authaudit.Row) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, row)
	return nil
}

type fixture struct {
	handlers   *Handlers
	checker    *authcheck.Checker
	issuer     *authtoken.Issuer
	config     *tenantconfig.Store
	audit      *recordingAudit
	domain     string
	tenantID   string
	tenantSlug string
	schema     string
	adminRole  string
	userID     string
	conn       *sql.DB
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

	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

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
	configStore := tenantconfig.NewStore(conn)
	if err := configStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}
	roleStore := role.NewStore(conn)
	apiKeys := apikey.NewStore(conn)
	if err := apiKeys.Bootstrap(ctx); err != nil {
		t.Fatalf("apikey Bootstrap() error: %v", err)
	}
	billingStore := billing.NewStore(conn)
	if err := billingStore.Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}

	signingKeyStore := signingkey.NewStore(conn, &secrets.EnvBackend{})
	if err := signingKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("signingkey Bootstrap() error: %v", err)
	}
	signingKeySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("signingkey LoadOrGenerate() error: %v", err)
	}
	rowCryptStore := rowcrypt.NewStore(conn, &secrets.EnvBackend{})
	if err := rowCryptStore.Bootstrap(ctx); err != nil {
		t.Fatalf("rowcrypt Bootstrap() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.row_encryption_keys`) })
	rowKeys, err := rowCryptStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("rowcrypt LoadOrGenerate() error: %v", err)
	}

	tenantResolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)
	issuer := authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore)
	roleCache := permcache.NewRoleCache(cacheClient)
	roleMap := permcache.NewRolePermissionMap()
	checker := authcheck.NewChecker(&signingKeySet.Active, sessionrevoke.NewRevoker(sessionStore, cacheClient), userStore, roleStore, roleCache, roleMap, apiKeys, false, nil, mfaStore, enforce.NewStore(configStore))
	audit := &recordingAudit{}
	handlers := NewHandlers(tenantResolver, checker, userStore, mfaStore, sessionStore, issuer,
		totp.NewService(mfaStore, rowKeys, cacheClient), recoverycode.NewService(mfaStore), audit)

	slug := fmt.Sprintf("mfaenrolltest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "MFA Enroll Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })
	if _, err := tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate fixture tenant: %v", err)
	}
	domain := slug + ".goerp.test"
	if _, err := tenantStore.CreateDomain(ctx, tt.ID, domain, tenant.DomainSubdomain, true); err != nil {
		t.Fatalf("CreateDomain() error: %v", err)
	}

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
	if err := roleMap.RebuildAll(ctx, tenantStore, roleStore, permission.NewPermissionRegistry()); err != nil {
		t.Fatalf("RebuildAll() error: %v", err)
	}

	f := &fixture{
		handlers:   handlers,
		checker:    checker,
		issuer:     issuer,
		config:     configStore,
		audit:      audit,
		domain:     domain,
		tenantID:   tt.ID,
		tenantSlug: slug,
		schema:     schema,
		adminRole:  roleID,
		conn:       conn,
	}
	f.userID = f.createMember(t)
	return f
}

// createMember adds an active user holding the admin role in the fixture's
// tenant.
func (f *fixture) createMember(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	email := fmt.Sprintf("member%d@%s.example.com", time.Now().UnixNano(), f.tenantSlug)
	userID, err := user.NewStore(f.conn).FindOrCreateInvited(ctx, email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.conn.Exec(`DELETE FROM system.user_mfa WHERE user_id = $1`, userID)
		_, _ = f.conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID)
		_, _ = f.conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID)
	})
	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, userID); err != nil {
		t.Fatalf("activate user: %v", err)
	}
	if _, err := f.conn.Exec(fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", f.schema), userID, f.adminRole); err != nil {
		t.Fatalf("grant admin role: %v", err)
	}
	return userID
}

func lockSharedKeyTables(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, name := range []string{"test.jwt_signing_keys_table", "test.row_encryption_keys_table"} {
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

func (f *fixture) requireMFA(t *testing.T) {
	t.Helper()
	if err := f.config.Set(context.Background(), f.tenantID, "mfa.enforcement_mode", string(enforce.ModeRequired)); err != nil {
		t.Fatalf("set mfa policy: %v", err)
	}
}

func (f *fixture) login(t *testing.T, userID string) string {
	t.Helper()
	tokens, err := f.issuer.Issue(context.Background(), authtoken.LoginParams{
		UserID:     userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

func (f *fixture) authContext(t *testing.T, accessToken string) *authcheck.AuthContext {
	t.Helper()
	authCtx, err := f.checker.Authenticate(context.Background(), accessToken, f.tenantID, f.tenantSlug, "203.0.113.7", nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		t.Fatalf("Authenticate() = %+v, %v; want an authenticated context", authCtx, err)
	}
	return authCtx
}

func (f *fixture) post(t *testing.T, handler http.HandlerFunc, path, accessToken string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(http.MethodPost, path, reader)
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("X-Client-Type", "cli")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", rec.Body.String(), err)
	}
	return rec.Code, resp
}

// begin starts an enrollment for accessToken's user and returns its id and
// secret.
func (f *fixture) begin(t *testing.T, accessToken string) (id, secret string) {
	t.Helper()
	status, resp := f.post(t, f.handlers.Begin, "/auth/mfa/enroll/totp", accessToken, nil)
	if status != http.StatusOK {
		t.Fatalf("Begin status = %d, want 200; body = %v", status, resp)
	}
	id, _ = resp["enrollment_id"].(string)
	secret, _ = resp["secret"].(string)
	if id == "" || secret == "" {
		t.Fatalf("Begin response = %v, want enrollment_id and secret", resp)
	}
	return id, secret
}

// enrollFirst enrolls the user's first factor through the handlers and
// returns the MFA-verified access token the confirm reissued.
func (f *fixture) enrollFirst(t *testing.T, accessToken string) string {
	t.Helper()
	id, secret := f.begin(t, accessToken)
	status, resp := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", accessToken, map[string]any{"enrollment_id": id, "code": currentCode(t, secret)})
	if status != http.StatusOK {
		t.Fatalf("first confirm status = %d; body = %v", status, resp)
	}
	token, _ := resp["access_token"].(string)
	return token
}

func currentCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := pquernatotp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}
	return code
}

func (f *fixture) countActive(t *testing.T, userID string, credType mfa.CredentialType) int {
	t.Helper()
	var n int
	if err := f.conn.QueryRow(`SELECT count(*) FROM system.user_mfa WHERE user_id = $1 AND type = $2 AND revoked_at IS NULL`, userID, credType).Scan(&n); err != nil {
		t.Fatalf("count user_mfa rows: %v", err)
	}
	return n
}

func TestBegin_AbandonedEnrollmentLeavesUserUnenrolled(t *testing.T) {
	f := newFixture(t)
	f.requireMFA(t)
	token := f.login(t, f.userID)

	status, resp := f.post(t, f.handlers.Begin, "/auth/mfa/enroll/totp", token, nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %v", status, resp)
	}
	if svg, _ := resp["qr_svg"].(string); len(svg) < 4 || svg[:4] != "<svg" {
		t.Errorf("qr_svg = %q, want an SVG document", svg)
	}

	if n := f.countActive(t, f.userID, mfa.CredentialTOTP); n != 0 {
		t.Errorf("active totp rows after Begin = %d, want 0", n)
	}
	required, err := f.checker.MFASetupRequired(context.Background(), f.tenantID, f.authContext(t, token))
	if err != nil || !required {
		t.Errorf("MFASetupRequired() = %v, %v; want true", required, err)
	}
}

func TestConfirm_FirstFactorIssuesCodesAndSatisfiesEnforcement(t *testing.T) {
	f := newFixture(t)
	f.requireMFA(t)
	token := f.login(t, f.userID)
	id, secret := f.begin(t, token)

	status, resp := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", token, map[string]any{
		"enrollment_id": id, "code": currentCode(t, secret), "label": "iPhone",
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %v", status, resp)
	}
	codes, _ := resp["recovery_codes"].([]any)
	if len(codes) != 10 {
		t.Errorf("recovery_codes = %v, want 10 codes", resp["recovery_codes"])
	}
	if n := f.countActive(t, f.userID, mfa.CredentialTOTP); n != 1 {
		t.Errorf("active totp rows = %d, want 1", n)
	}
	if n := f.countActive(t, f.userID, mfa.CredentialRecoveryCode); n != 10 {
		t.Errorf("active recovery_code rows = %d, want 10", n)
	}

	newToken, _ := resp["access_token"].(string)
	authCtx := f.authContext(t, newToken)
	if !slices.Contains(authCtx.AMR, "totp") {
		t.Errorf("reissued token amr = %v, want it to include totp", authCtx.AMR)
	}
	decision, err := f.checker.EnforceMFA(context.Background(), "/items", f.tenantID, authCtx)
	if err != nil || decision != enforce.Allowed {
		t.Errorf("EnforceMFA() with the reissued token = %q, %v; want allowed", decision, err)
	}
	if required, err := f.checker.MFASetupRequired(context.Background(), f.tenantID, authCtx); err != nil || required {
		t.Errorf("MFASetupRequired() after confirm = %v, %v; want false", required, err)
	}

	if len(f.audit.rows) != 1 || f.audit.rows[0].EventType != "mfa.enrolled" || f.audit.rows[0].UserID != f.userID {
		t.Errorf("audit rows = %+v, want one mfa.enrolled for the user", f.audit.rows)
	}
}

func TestConfirm_SecondFactorReturnsNullRecoveryCodes(t *testing.T) {
	f := newFixture(t)
	token := f.login(t, f.userID)

	verifiedToken := f.enrollFirst(t, token)

	id2, secret2 := f.begin(t, verifiedToken)
	status, resp := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", verifiedToken, map[string]any{"enrollment_id": id2, "code": currentCode(t, secret2)})
	if status != http.StatusOK {
		t.Fatalf("second confirm status = %d; body = %v", status, resp)
	}
	if v, present := resp["recovery_codes"]; !present || v != nil {
		t.Errorf("recovery_codes = %v (present %v), want null", v, present)
	}
	if n := f.countActive(t, f.userID, mfa.CredentialRecoveryCode); n != 10 {
		t.Errorf("active recovery_code rows = %d, want 10", n)
	}
	if n := f.countActive(t, f.userID, mfa.CredentialTOTP); n != 2 {
		t.Errorf("active totp rows = %d, want 2", n)
	}
}

func TestBeginAndConfirm_EnrolledUserNeedsMFAInThisSession(t *testing.T) {
	f := newFixture(t)
	passwordOnly := f.login(t, f.userID)
	verifiedToken := f.enrollFirst(t, passwordOnly)

	// A second login has only "pwd" in amr, like a stolen password session.
	fresh := f.login(t, f.userID)
	if status, resp := f.post(t, f.handlers.Begin, "/auth/mfa/enroll/totp", fresh, nil); status != http.StatusForbidden || errorCode(resp) != "mfa_required" {
		t.Errorf("Begin with a password-only session: status = %d, body = %v; want 403 mfa_required", status, resp)
	}
	id, secret := f.begin(t, verifiedToken)
	if status, resp := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", fresh, map[string]any{"enrollment_id": id, "code": currentCode(t, secret)}); status != http.StatusForbidden || errorCode(resp) != "mfa_required" {
		t.Errorf("Confirm with a password-only session: status = %d, body = %v; want 403 mfa_required", status, resp)
	}

	// A session whose MFA assurance is older than the policy's max age.
	authCtx := f.authContext(t, verifiedToken)
	stale := time.Now().Add(-48 * time.Hour)
	staleToken, _, err := f.issuer.ReissueAccessToken(authCtx.SessionID, f.tenantID, f.userID, authCtx.RolesLive, "totp", &stale)
	if err != nil {
		t.Fatalf("ReissueAccessToken() error: %v", err)
	}
	if status, resp := f.post(t, f.handlers.Begin, "/auth/mfa/enroll/totp", staleToken, nil); status != http.StatusForbidden || errorCode(resp) != "mfa_reverify_required" {
		t.Errorf("Begin with stale assurance: status = %d, body = %v; want 403 mfa_reverify_required", status, resp)
	}
	if n := f.countActive(t, f.userID, mfa.CredentialTOTP); n != 1 {
		t.Errorf("active totp rows = %d, want 1", n)
	}
}

func TestConfirm_WrongLengthCodeIsInvalidCode(t *testing.T) {
	f := newFixture(t)
	token := f.login(t, f.userID)
	id, _ := f.begin(t, token)

	status, resp := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", token, map[string]any{"enrollment_id": id, "code": "12345"})
	if status != http.StatusBadRequest || errorCode(resp) != "invalid_mfa_code" {
		t.Errorf("status = %d, body = %v; want 400 invalid_mfa_code", status, resp)
	}
}

func TestConfirm_WrongCodesThenEnrollmentDiscarded(t *testing.T) {
	f := newFixture(t)
	token := f.login(t, f.userID)
	id, secret := f.begin(t, token)
	wrong := "000000"
	if wrong == currentCode(t, secret) {
		wrong = "111111"
	}

	for range 5 {
		status, resp := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", token, map[string]any{"enrollment_id": id, "code": wrong})
		if status != http.StatusBadRequest || errorCode(resp) != "invalid_mfa_code" {
			t.Fatalf("wrong code: status = %d, body = %v; want 400 invalid_mfa_code", status, resp)
		}
	}
	status, resp := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", token, map[string]any{"enrollment_id": id, "code": currentCode(t, secret)})
	if status != http.StatusNotFound || errorCode(resp) != "mfa_enrollment_not_found" {
		t.Errorf("after 5 wrong codes: status = %d, body = %v; want 404 mfa_enrollment_not_found", status, resp)
	}
	if n := f.countActive(t, f.userID, mfa.CredentialTOTP); n != 0 {
		t.Errorf("active totp rows = %d, want 0", n)
	}
}

func TestConfirm_AnotherUsersSessionCannotConfirm(t *testing.T) {
	f := newFixture(t)
	ownerToken := f.login(t, f.userID)
	id, secret := f.begin(t, ownerToken)

	otherToken := f.login(t, f.createMember(t))
	status, resp := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", otherToken, map[string]any{"enrollment_id": id, "code": currentCode(t, secret)})
	if status != http.StatusNotFound || errorCode(resp) != "mfa_enrollment_not_found" {
		t.Errorf("status = %d, body = %v; want 404 mfa_enrollment_not_found", status, resp)
	}
}

func TestBeginAndConfirm_RequireAnAccessToken(t *testing.T) {
	f := newFixture(t)
	if status, _ := f.post(t, f.handlers.Begin, "/auth/mfa/enroll/totp", "", nil); status != http.StatusUnauthorized {
		t.Errorf("Begin without token status = %d, want 401", status)
	}
	if status, _ := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", "", map[string]any{"enrollment_id": "x", "code": "123456"}); status != http.StatusUnauthorized {
		t.Errorf("Confirm without token status = %d, want 401", status)
	}
}

func errorCode(resp map[string]any) string {
	e, _ := resp["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}
