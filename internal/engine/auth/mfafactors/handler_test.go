package mfafactors

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
	"golang.org/x/crypto/bcrypt"

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
	"github.com/djangbahevans/goerp/internal/engine/mfa/lockout"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/revoke"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

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

func (a *recordingAudit) events() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.rows))
	for i, r := range a.rows {
		out[i] = r.EventType
	}
	return out
}

type fixture struct {
	handlers   *Handlers
	checker    *authcheck.Checker
	issuer     *authtoken.Issuer
	config     *tenantconfig.Store
	mfa        *mfa.Store
	recovery   *recoverycode.Service
	lockout    *lockout.Counter
	rowKeys    *rowcrypt.RowKeySet
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
	ctx := t.Context()

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
	roleMap := permcache.NewRolePermissionMap()
	revoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)
	policies := enforce.NewStore(configStore)
	checker := authcheck.NewChecker(&signingKeySet.Active, revoker, userStore, roleStore, permcache.NewRoleCache(cacheClient), roleMap, apiKeys, false, nil, mfaStore, policies)
	recovery := recoverycode.NewService(mfaStore)
	lockoutCounter := lockout.NewCounter(cacheClient)
	audit := &recordingAudit{}
	handlers := NewHandlers(tenantResolver, checker, mfaStore, policies, totp.NewService(mfaStore, rowKeys, cacheClient), recovery, lockoutCounter, revoke.NewService(mfaStore, revoker), revoker, audit)

	slug := fmt.Sprintf("mfafactorstest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "MFA Factors Test Co")
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
		mfa:        mfaStore,
		recovery:   recovery,
		lockout:    lockoutCounter,
		rowKeys:    rowKeys,
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

func lockSharedKeyTables(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := t.Context()
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

// createMember adds an active user holding the admin role in the fixture's
// tenant.
func (f *fixture) createMember(t *testing.T) string {
	t.Helper()
	email := fmt.Sprintf("member%d@%s.example.com", time.Now().UnixNano(), f.tenantSlug)
	userID, err := user.NewStore(f.conn).FindOrCreateInvited(t.Context(), email)
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

func (f *fixture) requireMFA(t *testing.T) {
	t.Helper()
	if err := f.config.Set(t.Context(), f.tenantID, "mfa.enforcement_mode", string(enforce.ModeRequired)); err != nil {
		t.Fatalf("set mfa policy: %v", err)
	}
}

// login issues a new session family for the fixture's user on its own
// device and returns its access token.
func (f *fixture) login(t *testing.T) string {
	t.Helper()
	tokens, err := f.issuer.Issue(t.Context(), authtoken.LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano()%1e12),
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

func (f *fixture) sessionValid(t *testing.T, accessToken string) bool {
	t.Helper()
	authCtx, err := f.checker.Authenticate(t.Context(), accessToken, f.tenantID, f.tenantSlug, "203.0.113.7", nil, nil)
	return err == nil && authCtx.IsAuthenticated
}

// seedTOTP enrolls a TOTP factor for the fixture's user and returns its row
// and a currently valid code for it.
func (f *fixture) seedTOTP(t *testing.T, label string) (*mfa.Credential, string) {
	t.Helper()
	key, err := pquernatotp.Generate(pquernatotp.GenerateOpts{Issuer: "GoERP", AccountName: "user@example.com"})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	ciphertext, err := f.rowKeys.Encrypt([]byte(key.Secret()))
	if err != nil {
		t.Fatalf("encrypt totp secret: %v", err)
	}
	cred, err := f.mfa.Insert(t.Context(), f.userID, mfa.CredentialTOTP, ciphertext, &label)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	code, err := pquernatotp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}
	return cred, code
}

// seedRecoveryCode stores one recovery code, hashed at bcrypt's minimum
// cost to keep the tests fast.
func (f *fixture) seedRecoveryCode(t *testing.T, code string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash recovery code: %v", err)
	}
	if _, err := f.mfa.Insert(t.Context(), f.userID, mfa.CredentialRecoveryCode, hash, nil); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
}

func (f *fixture) countActive(t *testing.T, credType mfa.CredentialType) int {
	t.Helper()
	var n int
	if err := f.conn.QueryRow(`SELECT count(*) FROM system.user_mfa WHERE user_id = $1 AND type = $2 AND revoked_at IS NULL`, f.userID, credType).Scan(&n); err != nil {
		t.Fatalf("count user_mfa rows: %v", err)
	}
	return n
}

func (f *fixture) do(t *testing.T, handler http.HandlerFunc, method, path, accessToken string, params map[string]string, body any) (int, map[string]any) {
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
	req := httptest.NewRequest(method, path, reader)
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("X-Client-Type", "cli")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if params != nil {
		req = req.WithContext(route.WithParams(req.Context(), params))
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	var resp map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, resp
}

func (f *fixture) list(t *testing.T, accessToken string) map[string]any {
	t.Helper()
	status, resp := f.do(t, f.handlers.List, http.MethodGet, "/auth/mfa/factors", accessToken, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("List status = %d, want 200; body = %v", status, resp)
	}
	return resp
}

func (f *fixture) remove(t *testing.T, accessToken, factorID string, body any) (int, map[string]any) {
	t.Helper()
	return f.do(t, f.handlers.Remove, http.MethodPost, "/auth/mfa/factors/"+factorID+"/remove", accessToken, map[string]string{"id": factorID}, body)
}

func (f *fixture) regenerate(t *testing.T, accessToken string, body any) (int, map[string]any) {
	t.Helper()
	return f.do(t, f.handlers.RegenerateRecoveryCodes, http.MethodPost, "/auth/mfa/recovery-codes/regenerate", accessToken, nil, body)
}

func errorCode(resp map[string]any) string {
	e, _ := resp["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestList_ReturnsFactorsRecoveryCountAndPolicy(t *testing.T) {
	f := newFixture(t)
	cred, _ := f.seedTOTP(t, "iPhone")
	f.seedRecoveryCode(t, "AAAAA-BBBBB")
	f.seedRecoveryCode(t, "CCCCC-DDDDD")
	token := f.login(t)

	resp := f.list(t, token)
	factors, _ := resp["factors"].([]any)
	if len(factors) != 1 {
		t.Fatalf("factors = %v, want the one TOTP factor", resp["factors"])
	}
	factor, _ := factors[0].(map[string]any)
	if factor["id"] != cred.ID || factor["type"] != "totp" || factor["label"] != "iPhone" || factor["created_at"] == nil {
		t.Errorf("factor = %v, want id %s, type totp, label iPhone, created_at set", factor, cred.ID)
	}
	if v, ok := factor["last_used_at"]; !ok || v != nil {
		t.Errorf("last_used_at = %v (present %v), want null", v, ok)
	}
	if resp["recovery_codes_remaining"] != float64(2) {
		t.Errorf("recovery_codes_remaining = %v, want 2", resp["recovery_codes_remaining"])
	}
	if resp["required_by_policy"] != false {
		t.Errorf("required_by_policy = %v, want false under the default optional policy", resp["required_by_policy"])
	}

	f.requireMFA(t)
	if got := f.list(t, token)["required_by_policy"]; got != true {
		t.Errorf("required_by_policy = %v, want true under a required policy", got)
	}
}

func TestList_NoFactorsReturnsEmptyArray(t *testing.T) {
	f := newFixture(t)
	resp := f.list(t, f.login(t))
	if factors, ok := resp["factors"].([]any); !ok || len(factors) != 0 {
		t.Errorf("factors = %#v, want []", resp["factors"])
	}
}

func TestRemove_LastFactorUnderOptionalPolicyRevokesCodesAndEverySession(t *testing.T) {
	f := newFixture(t)
	cred, code := f.seedTOTP(t, "iPhone")
	f.seedRecoveryCode(t, "AAAAA-BBBBB")
	caller := f.login(t)
	other := f.login(t)

	status, resp := f.remove(t, caller, cred.ID, map[string]any{"type": "totp", "code": code})
	if status != http.StatusNoContent {
		t.Fatalf("Remove status = %d, want 204; body = %v", status, resp)
	}

	if n := f.countActive(t, mfa.CredentialTOTP); n != 0 {
		t.Errorf("active totp factors = %d, want 0", n)
	}
	if n := f.countActive(t, mfa.CredentialRecoveryCode); n != 0 {
		t.Errorf("active recovery codes = %d, want 0 after removing the last factor", n)
	}
	if f.sessionValid(t, caller) || f.sessionValid(t, other) {
		t.Error("a session still authenticates after removing a factor, want every session revoked")
	}
	if got := f.audit.events(); !slices.Equal(got, []string{"mfa.revoked"}) {
		t.Errorf("audit events = %v, want [mfa.revoked]", got)
	}

	fresh := f.login(t)
	authCtx, err := f.checker.Authenticate(t.Context(), fresh, f.tenantID, f.tenantSlug, "203.0.113.7", nil, nil)
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	setupRequired, err := f.checker.MFASetupRequired(t.Context(), f.tenantID, authCtx)
	if err != nil || setupRequired {
		t.Errorf("MFASetupRequired() = %v, %v; want false, nil", setupRequired, err)
	}
	if factors, _ := f.list(t, fresh)["factors"].([]any); len(factors) != 0 {
		t.Errorf("factors after removal = %v, want none", factors)
	}
}

func TestRemove_WithRecoveryCodeKeepsOtherFactorAndCodes(t *testing.T) {
	f := newFixture(t)
	lost, _ := f.seedTOTP(t, "Old phone")
	f.seedTOTP(t, "New phone")
	f.seedRecoveryCode(t, "AAAAA-BBBBB")
	f.seedRecoveryCode(t, "CCCCC-DDDDD")
	token := f.login(t)

	status, resp := f.remove(t, token, lost.ID, map[string]any{"type": "recovery_code", "code": "AAAAA-BBBBB"})
	if status != http.StatusNoContent {
		t.Fatalf("Remove status = %d, want 204; body = %v", status, resp)
	}
	if n := f.countActive(t, mfa.CredentialTOTP); n != 1 {
		t.Errorf("active totp factors = %d, want the other 1", n)
	}
	if n := f.countActive(t, mfa.CredentialRecoveryCode); n != 1 {
		t.Errorf("active recovery codes = %d, want 1 (the used one consumed, the other kept)", n)
	}
	if f.sessionValid(t, token) {
		t.Error("caller's session still authenticates after removing a factor, want revoked")
	}
}

func TestRemove_LastFactorUnderRequiredPolicyChangesNothing(t *testing.T) {
	f := newFixture(t)
	f.requireMFA(t)
	cred, _ := f.seedTOTP(t, "iPhone")
	f.seedRecoveryCode(t, "AAAAA-BBBBB")
	token := f.login(t)

	status, resp := f.remove(t, token, cred.ID, map[string]any{"type": "recovery_code", "code": "AAAAA-BBBBB"})
	if status != http.StatusConflict || errorCode(resp) != "mfa_required_by_policy" {
		t.Fatalf("Remove = %d %v, want 409 mfa_required_by_policy", status, resp)
	}
	if f.countActive(t, mfa.CredentialTOTP) != 1 || f.countActive(t, mfa.CredentialRecoveryCode) != 1 {
		t.Error("a credential was revoked or the recovery code consumed, want nothing changed")
	}
	if !f.sessionValid(t, token) {
		t.Error("session revoked by a refused removal, want untouched")
	}
	if got := f.audit.events(); len(got) != 0 {
		t.Errorf("audit events = %v, want none", got)
	}
}

func TestRemove_UnknownOrForeignFactorIsNotFound(t *testing.T) {
	f := newFixture(t)
	_, code := f.seedTOTP(t, "iPhone")
	token := f.login(t)

	other := f.userID
	f.userID = f.createMember(t)
	foreign, _ := f.seedTOTP(t, "Theirs")
	f.userID = other

	for _, id := range []string{"not-a-uuid", "00000000-0000-4000-8000-000000000000", foreign.ID} {
		status, resp := f.remove(t, token, id, map[string]any{"type": "totp", "code": code})
		if status != http.StatusNotFound || errorCode(resp) != "mfa_factor_not_found" {
			t.Errorf("Remove(%s) = %d %v, want 404 mfa_factor_not_found", id, status, resp)
		}
	}
	if !f.sessionValid(t, token) {
		t.Error("session revoked by a failed removal, want untouched")
	}
}

func TestRemove_WrongCodeCountsTowardReverifyLockout(t *testing.T) {
	f := newFixture(t)
	cred, _ := f.seedTOTP(t, "iPhone")
	token := f.login(t)

	for i := range 5 {
		status, resp := f.remove(t, token, cred.ID, map[string]any{"type": "totp", "code": "000000"})
		if status != http.StatusUnauthorized || errorCode(resp) != "invalid_mfa_code" {
			t.Fatalf("attempt %d: Remove = %d %v, want 401 invalid_mfa_code", i+1, status, resp)
		}
	}
	locked, err := f.lockout.Locked(t.Context(), f.userID, f.tenantID)
	if err != nil || !locked {
		t.Fatalf("lockout.Locked() = %v, %v; want true after 5 failures", locked, err)
	}
	status, resp := f.remove(t, token, cred.ID, map[string]any{"type": "totp", "code": "000000"})
	if status != http.StatusLocked || errorCode(resp) != "mfa_locked" {
		t.Errorf("Remove once locked = %d %v, want 423 mfa_locked", status, resp)
	}
	if n := f.countActive(t, mfa.CredentialTOTP); n != 1 {
		t.Errorf("active totp factors = %d, want 1", n)
	}
}

func TestRegenerate_ReplacesCodesAndRevokesOnlyOtherSessions(t *testing.T) {
	f := newFixture(t)
	_, code := f.seedTOTP(t, "iPhone")
	f.seedRecoveryCode(t, "AAAAA-BBBBB")
	caller := f.login(t)
	other := f.login(t)

	status, resp := f.regenerate(t, caller, map[string]any{"type": "totp", "code": code})
	if status != http.StatusOK {
		t.Fatalf("Regenerate status = %d, want 200; body = %v", status, resp)
	}
	codes, _ := resp["recovery_codes"].([]any)
	if len(codes) != 10 {
		t.Fatalf("recovery_codes = %v, want ten codes", resp["recovery_codes"])
	}

	if got := f.list(t, caller)["recovery_codes_remaining"]; got != float64(10) {
		t.Errorf("recovery_codes_remaining = %v, want 10", got)
	}

	ctx := t.Context()
	if ok, _, _ := f.recovery.Verify(ctx, f.userID, "AAAAA-BBBBB"); ok {
		t.Error("old recovery code still verifies, want revoked")
	}
	newCode, _ := codes[0].(string)
	if ok, _, err := f.recovery.Verify(ctx, f.userID, newCode); err != nil || !ok {
		t.Errorf("new recovery code Verify() = %v, %v; want true", ok, err)
	}
	if !f.sessionValid(t, caller) {
		t.Error("caller's session revoked by regeneration, want kept")
	}
	if f.sessionValid(t, other) {
		t.Error("other session still authenticates after regeneration, want revoked")
	}
	if got := f.audit.events(); !slices.Equal(got, []string{"mfa.recovery_codes_regenerated"}) {
		t.Errorf("audit events = %v, want [mfa.recovery_codes_regenerated]", got)
	}
}

func TestRegenerate_RecoveryCodeIsConsumed(t *testing.T) {
	f := newFixture(t)
	f.seedTOTP(t, "iPhone")
	f.seedRecoveryCode(t, "AAAAA-BBBBB")
	token := f.login(t)

	status, resp := f.regenerate(t, token, map[string]any{"type": "recovery_code", "code": "AAAAA-BBBBB"})
	if status != http.StatusOK {
		t.Fatalf("Regenerate status = %d, want 200; body = %v", status, resp)
	}
	status, resp = f.regenerate(t, token, map[string]any{"type": "recovery_code", "code": "AAAAA-BBBBB"})
	if status != http.StatusUnauthorized || errorCode(resp) != "invalid_mfa_code" {
		t.Errorf("reusing the recovery code = %d %v, want 401 invalid_mfa_code", status, resp)
	}
}

func TestRegenerate_WithoutFactorIsNotEnrolled(t *testing.T) {
	f := newFixture(t)
	f.seedRecoveryCode(t, "AAAAA-BBBBB")
	token := f.login(t)

	status, resp := f.regenerate(t, token, map[string]any{"type": "recovery_code", "code": "AAAAA-BBBBB"})
	if status != http.StatusConflict || errorCode(resp) != "mfa_not_enrolled" {
		t.Fatalf("Regenerate = %d %v, want 409 mfa_not_enrolled", status, resp)
	}
	if n := f.countActive(t, mfa.CredentialRecoveryCode); n != 1 {
		t.Errorf("active recovery codes = %d, want the original 1 unconsumed", n)
	}
}

func TestHandlers_RequireAccessTokenAndCodeBody(t *testing.T) {
	f := newFixture(t)
	cred, _ := f.seedTOTP(t, "iPhone")

	if status, _ := f.do(t, f.handlers.List, http.MethodGet, "/auth/mfa/factors", "", nil, nil); status != http.StatusUnauthorized {
		t.Errorf("List without a token = %d, want 401", status)
	}
	if status, _ := f.remove(t, "", cred.ID, map[string]any{"type": "totp", "code": "123456"}); status != http.StatusUnauthorized {
		t.Errorf("Remove without a token = %d, want 401", status)
	}
	token := f.login(t)
	if status, resp := f.regenerate(t, token, map[string]any{"type": "totp"}); status != http.StatusBadRequest || errorCode(resp) != "invalid_request" {
		t.Errorf("Regenerate without a code = %d %v, want 400 invalid_request", status, resp)
	}
}
