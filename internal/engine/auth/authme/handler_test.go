package authme

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// fixture mirrors mfareverify's own fixture shape, trimmed to what a
// plain "is there a valid session" check needs, plus the MFA stores the
// mfa_setup_required flag reads — no rowcrypt.
type fixture struct {
	handler     *Handler
	issuer      *authtoken.Issuer
	revoker     *sessionrevoke.Revoker
	tenantStore *tenant.Store
	filesStore  *files.Store
	config      *tenantconfig.Store
	domain      string
	tenantID    string
	tenantSlug  string
	userID      string
	userEmail   string
	conn        *sql.DB
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

	tenantResolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)
	issuer := authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore)
	roleCache := permcache.NewRoleCache(cacheClient)
	roleMap := permcache.NewRolePermissionMap()
	revoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)
	mfaStore := mfa.NewStore(conn)
	if err := mfaStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfa Bootstrap() error: %v", err)
	}
	configStore := tenantconfig.NewStore(conn)
	if err := configStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}
	authChecker := authcheck.NewChecker(&signingKeySet.Active, revoker, userStore, roleStore, roleCache, roleMap, apiKeys, false, nil, mfaStore, enforce.NewStore(configStore))

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New() error: %v", err)
	}
	filesStore := files.NewStore(conn)

	handler := NewHandler(tenantResolver, authChecker, userStore, filesStore, backend, testAvailableLocales)

	slug := fmt.Sprintf("authmetest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Auth Me Test Co")
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

	email := slug + "@example.com"
	userID, err := userStore.FindOrCreateInvited(ctx, email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })
	if _, err := conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, userID); err != nil {
		t.Fatalf("activate fixture user: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })

	schema := tenantschema.Name(slug)
	if _, err := conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)) })
	if err := filesStore.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("files Bootstrap() error: %v", err)
	}
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
		handler:     handler,
		issuer:      issuer,
		revoker:     revoker,
		tenantStore: tenantStore,
		filesStore:  filesStore,
		config:      configStore,
		domain:      domain,
		tenantID:    tt.ID,
		tenantSlug:  slug,
		userID:      userID,
		userEmail:   email,
		conn:        conn,
	}
}

// newSiblingTenant creates a second, unrelated, active tenant+domain using
// the same fixture's already-open connection — a distinct newFixture(t)
// call would deadlock, since a second lockSharedKeyTable call would block
// forever waiting for this test's own first fixture to release the same
// advisory lock in its (not-yet-run) t.Cleanup.
func (f *fixture) newSiblingTenant(t *testing.T) (domain string) {
	t.Helper()
	ctx := context.Background()

	slug := fmt.Sprintf("authmesibling%d", time.Now().UnixNano())
	tt, err := f.tenantStore.CreateTenant(ctx, slug, "Auth Me Sibling Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })
	if _, err := f.tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate sibling tenant: %v", err)
	}

	domain = slug + ".goerp.test"
	if _, err := f.tenantStore.CreateDomain(ctx, tt.ID, domain, tenant.DomainSubdomain, true); err != nil {
		t.Fatalf("CreateDomain() error: %v", err)
	}
	return domain
}

// lockSharedKeyTable mirrors mfareverify's own lock helper — serializes
// this package's tests against every other package's test touching the
// same shared signing-key table.
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

func (f *fixture) issueAccessToken(t *testing.T) string {
	t.Helper()
	tokens, err := f.issuer.Issue(context.Background(), authtoken.LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

func (f *fixture) doMe(t *testing.T, host, accessToken string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Host = host
	req.RemoteAddr = "203.0.113.7:54321"
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestServeHTTP_ValidTokenReturnsUserAndTenant(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)

	rec := f.doMe(t, f.domain, accessToken)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.User.ID != f.userID {
		t.Errorf("user.id = %q, want %q", resp.User.ID, f.userID)
	}
	if resp.User.Email != f.userEmail {
		t.Errorf("user.email = %q, want %q", resp.User.Email, f.userEmail)
	}
	if len(resp.User.Roles) == 0 {
		t.Error("user.roles is empty, want the fixture's granted admin role")
	}
	if resp.User.ContactID != nil {
		t.Errorf("user.contact_id = %v, want nil (fixture user has no linked contact)", *resp.User.ContactID)
	}
	if resp.User.Name != nil {
		t.Errorf("user.name = %v, want nil (fixture user has no user_profiles row)", *resp.User.Name)
	}
	if resp.User.AvatarURL != nil {
		t.Errorf("user.avatar_url = %v, want nil", *resp.User.AvatarURL)
	}
	if resp.Tenant.ID != f.tenantID {
		t.Errorf("tenant.id = %q, want %q", resp.Tenant.ID, f.tenantID)
	}
	if resp.Tenant.Slug != f.tenantSlug {
		t.Errorf("tenant.slug = %q, want %q", resp.Tenant.Slug, f.tenantSlug)
	}
}

func TestServeHTTP_MFASetupRequiredFollowsPolicyAndEnrollment(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.user_mfa WHERE user_id = $1`, f.userID) })
	accessToken := f.issueAccessToken(t)

	setupRequired := func() bool {
		t.Helper()
		rec := f.doMe(t, f.domain, accessToken)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var resp meResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		return resp.User.MFASetupRequired
	}

	if setupRequired() {
		t.Error("mfa_setup_required = true under the default optional policy, want false")
	}
	if err := f.config.Set(ctx, f.tenantID, "mfa.enforcement_mode", string(enforce.ModeRequired)); err != nil {
		t.Fatalf("set mfa policy: %v", err)
	}
	if !setupRequired() {
		t.Error("mfa_setup_required = false under a required policy with no factor, want true")
	}
	if _, err := mfa.NewStore(f.conn).Insert(ctx, f.userID, mfa.CredentialTOTP, []byte("ciphertext"), nil); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	if setupRequired() {
		t.Error("mfa_setup_required = true after enrolling a factor, want false")
	}
}

func TestServeHTTP_ReturnsContactIDWhenUserIsLinkedToContact(t *testing.T) {
	f := newFixture(t)
	const contactID = "0198a3c2-7d4e-7c1a-9f3b-5e2d1a4b6c80"
	if _, err := f.conn.Exec(`UPDATE system.users SET contact_id = $1 WHERE id = $2`, contactID, f.userID); err != nil {
		t.Fatalf("link fixture user to contact: %v", err)
	}
	accessToken := f.issueAccessToken(t)

	rec := f.doMe(t, f.domain, accessToken)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.User.ContactID == nil || *resp.User.ContactID != contactID {
		t.Errorf("user.contact_id = %v, want %q", resp.User.ContactID, contactID)
	}
}

func TestServeHTTP_ReturnsNameWhenProfileExists(t *testing.T) {
	f := newFixture(t)
	if _, err := f.conn.Exec(`INSERT INTO system.user_profiles (user_id, name) VALUES ($1, $2)`, f.userID, "Ada Lovelace"); err != nil {
		t.Fatalf("insert fixture profile: %v", err)
	}
	accessToken := f.issueAccessToken(t)

	rec := f.doMe(t, f.domain, accessToken)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.User.Name == nil || *resp.User.Name != "Ada Lovelace" {
		t.Errorf("user.name = %v, want \"Ada Lovelace\"", resp.User.Name)
	}
	if resp.User.AvatarURL != nil {
		t.Errorf("user.avatar_url = %v, want nil (no avatar set)", *resp.User.AvatarURL)
	}
}

func TestServeHTTP_ReturnsResolvedAvatarURLWhenAvatarIsSet(t *testing.T) {
	f := newFixture(t)

	fileID := "00000000-0000-7000-8000-000000000001"
	if err := f.filesStore.Insert(context.Background(), f.tenantSlug, files.InsertRow{
		ID:           fileID,
		TenantID:     f.tenantID,
		StorageKey:   "avatars/" + f.tenantID + "/2026/01/" + fileID + ".png",
		OriginalName: "avatar.png",
		ContentType:  "image/png",
		SizeBytes:    123,
		Purpose:      "avatars",
	}); err != nil {
		t.Fatalf("insert fixture file: %v", err)
	}
	if _, err := f.conn.Exec(
		`INSERT INTO system.user_profiles (user_id, name, avatar_file_id) VALUES ($1, $2, $3)`,
		f.userID, "Ada Lovelace", fileID,
	); err != nil {
		t.Fatalf("insert fixture profile: %v", err)
	}
	accessToken := f.issueAccessToken(t)

	rec := f.doMe(t, f.domain, accessToken)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.User.AvatarURL == nil || *resp.User.AvatarURL == "" {
		t.Fatal("user.avatar_url is nil/empty, want a resolved URL")
	}
	if *resp.User.AvatarURL == fileID {
		t.Error("user.avatar_url is the raw file id, want a resolved signed URL")
	}
}

func TestServeHTTP_AvatarNilWhenFileRowMissing(t *testing.T) {
	f := newFixture(t)

	// avatar_file_id points at a file id that was never inserted into
	// this tenant's files table — files.Store.GetByID returns
	// ErrFileNotFound.
	fileID := "00000000-0000-7000-8000-000000000002"
	if _, err := f.conn.Exec(
		`INSERT INTO system.user_profiles (user_id, name, avatar_file_id) VALUES ($1, $2, $3)`,
		f.userID, "Ada Lovelace", fileID,
	); err != nil {
		t.Fatalf("insert fixture profile: %v", err)
	}
	accessToken := f.issueAccessToken(t)

	rec := f.doMe(t, f.domain, accessToken)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.User.AvatarURL != nil {
		t.Errorf("user.avatar_url = %v, want nil (no matching files row)", *resp.User.AvatarURL)
	}
}

func TestServeHTTP_AvatarNilWhenFileSoftDeleted(t *testing.T) {
	f := newFixture(t)

	fileID := "00000000-0000-7000-8000-000000000003"
	if err := f.filesStore.Insert(context.Background(), f.tenantSlug, files.InsertRow{
		ID:           fileID,
		TenantID:     f.tenantID,
		StorageKey:   "avatars/" + f.tenantID + "/2026/01/" + fileID + ".png",
		OriginalName: "avatar.png",
		ContentType:  "image/png",
		SizeBytes:    123,
		Purpose:      "avatars",
	}); err != nil {
		t.Fatalf("insert fixture file: %v", err)
	}
	if err := f.filesStore.MarkDeleted(context.Background(), f.tenantSlug, fileID); err != nil {
		t.Fatalf("MarkDeleted() error: %v", err)
	}
	if _, err := f.conn.Exec(
		`INSERT INTO system.user_profiles (user_id, name, avatar_file_id) VALUES ($1, $2, $3)`,
		f.userID, "Ada Lovelace", fileID,
	); err != nil {
		t.Fatalf("insert fixture profile: %v", err)
	}
	accessToken := f.issueAccessToken(t)

	rec := f.doMe(t, f.domain, accessToken)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.User.AvatarURL != nil {
		t.Errorf("user.avatar_url = %v, want nil (file is soft-deleted)", *resp.User.AvatarURL)
	}
}

func TestServeHTTP_AvatarNilWhenFilesStoreOrBackendMissing(t *testing.T) {
	f := newFixture(t)

	fileID := "00000000-0000-7000-8000-000000000004"
	if err := f.filesStore.Insert(context.Background(), f.tenantSlug, files.InsertRow{
		ID:           fileID,
		TenantID:     f.tenantID,
		StorageKey:   "avatars/" + f.tenantID + "/2026/01/" + fileID + ".png",
		OriginalName: "avatar.png",
		ContentType:  "image/png",
		SizeBytes:    123,
		Purpose:      "avatars",
	}); err != nil {
		t.Fatalf("insert fixture file: %v", err)
	}
	if _, err := f.conn.Exec(
		`INSERT INTO system.user_profiles (user_id, name, avatar_file_id) VALUES ($1, $2, $3)`,
		f.userID, "Ada Lovelace", fileID,
	); err != nil {
		t.Fatalf("insert fixture profile: %v", err)
	}
	accessToken := f.issueAccessToken(t)

	// A shallow copy with files/backend nilled out — same warn-only
	// dependency shape storage.New(cfg.StorageBackend) can leave engine.go
	// with in production (a down/misconfigured object storage backend).
	broken := *f.handler
	broken.files = nil
	broken.backend = nil

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("Authorization", "Bearer "+accessToken)
	rec := httptest.NewRecorder()
	broken.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.User.AvatarURL != nil {
		t.Errorf("user.avatar_url = %v, want nil (files/backend unavailable)", *resp.User.AvatarURL)
	}
}

// erroringBackend implements storage.Backend, failing only SignedURL —
// exercises AvatarURL's own degrade-on-backend-error branch, which
// the real LocalBackend's SignedURL never takes (it never fails).
type erroringBackend struct{ storage.Backend }

func (erroringBackend) SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	return "", fmt.Errorf("backend unavailable")
}

func TestServeHTTP_AvatarNilWhenSignedURLGenerationFails(t *testing.T) {
	f := newFixture(t)

	fileID := "00000000-0000-7000-8000-000000000005"
	if err := f.filesStore.Insert(context.Background(), f.tenantSlug, files.InsertRow{
		ID:           fileID,
		TenantID:     f.tenantID,
		StorageKey:   "avatars/" + f.tenantID + "/2026/01/" + fileID + ".png",
		OriginalName: "avatar.png",
		ContentType:  "image/png",
		SizeBytes:    123,
		Purpose:      "avatars",
	}); err != nil {
		t.Fatalf("insert fixture file: %v", err)
	}
	if _, err := f.conn.Exec(
		`INSERT INTO system.user_profiles (user_id, name, avatar_file_id) VALUES ($1, $2, $3)`,
		f.userID, "Ada Lovelace", fileID,
	); err != nil {
		t.Fatalf("insert fixture profile: %v", err)
	}
	accessToken := f.issueAccessToken(t)

	broken := *f.handler
	broken.backend = erroringBackend{}

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("Authorization", "Bearer "+accessToken)
	rec := httptest.NewRecorder()
	broken.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.User.AvatarURL != nil {
		t.Errorf("user.avatar_url = %v, want nil (SignedURL failed)", *resp.User.AvatarURL)
	}
}

func TestServeHTTP_NoTokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doMe(t, f.domain, "")

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_MalformedTokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doMe(t, f.domain, "not-a-real-token")

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_TokenForDifferentTenantRejected(t *testing.T) {
	f := newFixture(t)
	siblingDomain := f.newSiblingTenant(t)
	accessToken := f.issueAccessToken(t)

	rec := f.doMe(t, siblingDomain, accessToken)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_UnresolvableHostRejected(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)

	rec := f.doMe(t, "no-such-tenant.goerp.test", accessToken)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_RevokedSessionRejected(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)

	var sessionID string
	if err := f.conn.QueryRowContext(context.Background(),
		`SELECT id FROM system.sessions WHERE user_id = $1`, f.userID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("query fixture session id: %v", err)
	}
	// Authenticate checks the Redis blocklist, not sessions.revoked_at
	// directly — Revoke populates both, a raw UPDATE would populate
	// neither the JWT signature nor the blocklist, so the token would
	// keep validating.
	if err := f.revoker.Revoke(context.Background(), sessionID, "test"); err != nil {
		t.Fatalf("revoke fixture session: %v", err)
	}

	rec := f.doMe(t, f.domain, accessToken)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

// A deployment's own GOERP_AVAILABLE_LOCALES, not the platform default, so
// the tests show the configured list reaches the response.
var testAvailableLocales = []string{"en", "pt-BR"}

func (f *fixture) me(t *testing.T) meResponse {
	t.Helper()
	rec := f.doMe(t, f.domain, f.issueAccessToken(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestServeHTTP_ReportsDefaultPreferencesAndPlatformLocaleDefaults(t *testing.T) {
	f := newFixture(t)

	resp := f.me(t)

	if resp.User.Theme != "system" || resp.User.Locale != nil || resp.User.Timezone != nil || resp.User.DateFormat != nil {
		t.Errorf("preferences = %q/%v/%v/%v, want system and three nulls", resp.User.Theme, resp.User.Locale, resp.User.Timezone, resp.User.DateFormat)
	}
	if resp.Tenant.DefaultLocale != "en" || resp.Tenant.DefaultTimezone != "UTC" {
		t.Errorf("tenant defaults = %q/%q, want en/UTC", resp.Tenant.DefaultLocale, resp.Tenant.DefaultTimezone)
	}
	if strings.Join(resp.Tenant.AvailableLocales, ",") != "en,pt-BR" {
		t.Errorf("tenant.available_locales = %v, want the configured [en pt-BR]", resp.Tenant.AvailableLocales)
	}
}

func TestServeHTTP_ReportsStoredPreferences(t *testing.T) {
	f := newFixture(t)
	users := user.NewStore(f.conn)
	if _, err := users.UpdateProfile(t.Context(), f.userID, user.ProfileUpdate{
		Theme:      new("dark"),
		Locale:     user.NullableField{Set: true, Value: new("pt-BR")},
		Timezone:   user.NullableField{Set: true, Value: new("Africa/Accra")},
		DateFormat: user.NullableField{Set: true, Value: new("iso")},
	}); err != nil {
		t.Fatalf("UpdateProfile() error: %v", err)
	}

	resp := f.me(t)

	if resp.User.Theme != "dark" || deref(resp.User.Locale) != "pt-BR" || deref(resp.User.Timezone) != "Africa/Accra" || deref(resp.User.DateFormat) != "iso" {
		t.Errorf("preferences = %q/%v/%v/%v, want dark/pt-BR/Africa/Accra/iso", resp.User.Theme, resp.User.Locale, resp.User.Timezone, resp.User.DateFormat)
	}
	// UpdateProfile created the row without a name, so its "" placeholder
	// reads as no name.
	if resp.User.Name != nil {
		t.Errorf("user.name = %q, want nil for the placeholder name", *resp.User.Name)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
