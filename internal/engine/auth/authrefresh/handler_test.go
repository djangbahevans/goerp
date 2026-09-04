package authrefresh

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

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

type fixture struct {
	handler    *Handler
	issuer     *authtoken.Issuer
	tenantSlug string
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
	lockSharedKeyTable(t, conn)

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
	roleStore := role.NewStore(conn)

	signingKeyStore := signingkey.NewStore(conn, &secrets.EnvBackend{})
	if err := signingKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("signingkey Bootstrap() error: %v", err)
	}
	signingKeySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("signingkey LoadOrGenerate() error: %v", err)
	}

	issuer := authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore)
	handler := NewHandler(issuer)

	slug := fmt.Sprintf("authrefreshtest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Auth Refresh Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	userID, err := userStore.FindOrCreateInvited(ctx, slug+"@example.com")
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })
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

	return &fixture{
		handler:    handler,
		issuer:     issuer,
		tenantSlug: slug,
		userID:     userID,
		conn:       conn,
	}
}

// lockSharedKeyTable mirrors authme's own lock helper — serializes this
// package's tests against every other package's test touching the same
// shared signing-key table.
func lockSharedKeyTable(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	key := db.AdvisoryLockKey("test.jwt_signing_keys_table")
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire dedicated connection for signing-key lock: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatalf("acquire signing-key advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		_ = conn.Close()
	})
}

func (f *fixture) login(t *testing.T, deviceID string) *authtoken.Tokens {
	t.Helper()
	tokens, err := f.issuer.Issue(context.Background(), authtoken.LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   deviceID,
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens
}

func (f *fixture) doRefreshBrowser(t *testing.T, refreshCookie, deviceIDCookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	if refreshCookie != "" {
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshCookie})
	}
	if deviceIDCookie != "" {
		req.AddCookie(&http.Cookie{Name: "device_id", Value: deviceIDCookie})
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) doRefreshNonBrowser(t *testing.T, token, deviceID string) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if deviceID != "" {
		body, _ = json.Marshal(map[string]string{"device_id": deviceID})
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	req.Header.Set("X-Client-Type", "cli")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if len(body) > 0 {
		req.ContentLength = int64(len(body))
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestServeHTTP_Browser_LiveTokenRotatesAndSetsCookies(t *testing.T) {
	f := newFixture(t)
	tokens := f.login(t, "11111111-1111-1111-1111-111111111111")

	rec := f.doRefreshBrowser(t, tokens.RefreshToken, "11111111-1111-1111-1111-111111111111")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	cookies := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		cookies[c.Name] = c
	}
	access, ok := cookies["__Host-access_token"]
	if !ok || access.Value == "" {
		t.Error("__Host-access_token cookie missing or empty")
	}
	refresh, ok := cookies["refresh_token"]
	if !ok || refresh.Value == "" {
		t.Error("refresh_token cookie missing or empty")
	}
	if refresh != nil && refresh.Value == tokens.RefreshToken {
		t.Error("refresh_token cookie unchanged, want a freshly rotated token")
	}
	if _, ok := cookies["device_id"]; ok {
		t.Error("device_id cookie was set, want it left alone — refresh never mints a new device identity")
	}
}

func TestServeHTTP_NonBrowser_LiveTokenReturnsJSONTokens(t *testing.T) {
	f := newFixture(t)
	tokens := f.login(t, "11111111-1111-1111-1111-111111111111")

	rec := f.doRefreshNonBrowser(t, tokens.RefreshToken, "11111111-1111-1111-1111-111111111111")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	accessToken, _ := resp["access_token"].(string)
	refreshToken, _ := resp["refresh_token"].(string)
	if accessToken == "" {
		t.Error("access_token missing or empty")
	}
	if refreshToken == "" || refreshToken == tokens.RefreshToken {
		t.Error("refresh_token missing or unchanged")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("cookies = %v, want none for a non-browser client", rec.Result().Cookies())
	}
}

func TestServeHTTP_UnknownTokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doRefreshBrowser(t, "not-a-real-refresh-token", "")

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_NoTokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doRefreshBrowser(t, "", "")

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_ReplayedTokenFromDifferentDeviceRejected(t *testing.T) {
	f := newFixture(t)
	tokens := f.login(t, "11111111-1111-1111-1111-111111111111")

	first := f.doRefreshBrowser(t, tokens.RefreshToken, "11111111-1111-1111-1111-111111111111")
	if first.Code != http.StatusOK {
		t.Fatalf("first refresh status = %d, want 200; body = %s", first.Code, first.Body.String())
	}

	replay := f.doRefreshBrowser(t, tokens.RefreshToken, "22222222-2222-2222-2222-222222222222")

	if replay.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", replay.Code, replay.Body.String())
	}
}

func TestServeHTTP_NonBrowserMalformedBodyRejected(t *testing.T) {
	f := newFixture(t)
	tokens := f.login(t, "11111111-1111-1111-1111-111111111111")

	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader([]byte("{not json")))
	req.Header.Set("X-Client-Type", "cli")
	req.Header.Set("Authorization", "Bearer "+tokens.RefreshToken)
	req.ContentLength = int64(len("{not json"))
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}
