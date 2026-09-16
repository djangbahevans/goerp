package authmeupdate

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
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// fixture mirrors authme's own fixture shape.
type fixture struct {
	handler     *Handler
	issuer      *authtoken.Issuer
	tenantStore *tenant.Store
	filesStore  *files.Store
	userStore   *user.Store
	domain      string
	tenantID    string
	tenantSlug  string
	userID      string
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
	authChecker := authcheck.NewChecker(&signingKeySet.Active, revoker, userStore, roleStore, roleCache, roleMap, apiKeys, false, nil, nil, nil)

	filesStore := files.NewStore(conn)

	handler := NewHandler(tenantResolver, authChecker, userStore, filesStore)

	slug := fmt.Sprintf("authmeupdatetest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Auth Me Update Test Co")
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
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.user_profiles WHERE user_id = $1`, userID) })

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
		tenantStore: tenantStore,
		filesStore:  filesStore,
		userStore:   userStore,
		domain:      domain,
		tenantID:    tt.ID,
		tenantSlug:  slug,
		userID:      userID,
		conn:        conn,
	}
}

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

func (f *fixture) insertFile(t *testing.T, purpose string) string {
	t.Helper()
	fileID := fmt.Sprintf("00000000-0000-7000-8000-%012d", time.Now().UnixNano()%1_000_000_000_000)
	if err := f.filesStore.Insert(context.Background(), f.tenantSlug, files.InsertRow{
		ID:           fileID,
		TenantID:     f.tenantID,
		StorageKey:   purpose + "/" + f.tenantID + "/2026/01/" + fileID + ".png",
		OriginalName: "avatar.png",
		ContentType:  "image/png",
		SizeBytes:    100,
		Purpose:      purpose,
	}); err != nil {
		t.Fatalf("insert fixture file: %v", err)
	}
	return fileID
}

func (f *fixture) doPatch(t *testing.T, host, accessToken string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/auth/me", bytes.NewBufferString(body))
	req.Host = host
	req.RemoteAddr = "203.0.113.7:54321"
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestServeHTTP_SavesNameOnly(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)

	rec := f.doPatch(t, f.domain, accessToken, `{"name": "Ada Lovelace"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp updateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Name != "Ada Lovelace" {
		t.Errorf("response name = %q, want %q", resp.Name, "Ada Lovelace")
	}

	profile, err := f.userStore.GetProfile(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Name != "Ada Lovelace" {
		t.Errorf("stored name = %q, want %q", profile.Name, "Ada Lovelace")
	}
	if profile.AvatarFileID != nil {
		t.Errorf("stored AvatarFileID = %v, want nil", *profile.AvatarFileID)
	}
}

func TestServeHTTP_RenameOverwritesExistingName(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)

	if rec := f.doPatch(t, f.domain, accessToken, `{"name": "First Name"}`); rec.Code != http.StatusOK {
		t.Fatalf("first PATCH status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec := f.doPatch(t, f.domain, accessToken, `{"name": "Second Name"}`); rec.Code != http.StatusOK {
		t.Fatalf("second PATCH status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	profile, err := f.userStore.GetProfile(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.Name != "Second Name" {
		t.Errorf("stored name = %q, want %q", profile.Name, "Second Name")
	}
}

func TestServeHTTP_SetsAvatarFromRealUploadedFile(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)
	fileID := f.insertFile(t, "avatars")

	rec := f.doPatch(t, f.domain, accessToken, fmt.Sprintf(`{"name": "Ada Lovelace", "avatar_id": %q}`, fileID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	profile, err := f.userStore.GetProfile(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.AvatarFileID == nil || *profile.AvatarFileID != fileID {
		t.Errorf("stored AvatarFileID = %v, want %q", profile.AvatarFileID, fileID)
	}
}

func TestServeHTTP_UnknownAvatarIDRejected(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)

	rec := f.doPatch(t, f.domain, accessToken, `{"name": "Ada Lovelace", "avatar_id": "00000000-0000-7000-8000-000000000099"}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestServeHTTP_DeletedAvatarIDRejected(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)
	fileID := f.insertFile(t, "avatars")
	if err := f.filesStore.MarkDeleted(context.Background(), f.tenantSlug, fileID); err != nil {
		t.Fatalf("MarkDeleted() error: %v", err)
	}

	rec := f.doPatch(t, f.domain, accessToken, fmt.Sprintf(`{"name": "Ada Lovelace", "avatar_id": %q}`, fileID))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestServeHTTP_ReplacingAvatarMarksOldFileDeleted(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)
	oldFileID := f.insertFile(t, "avatars")
	newFileID := f.insertFile(t, "avatars")

	if rec := f.doPatch(t, f.domain, accessToken, fmt.Sprintf(`{"name": "Ada Lovelace", "avatar_id": %q}`, oldFileID)); rec.Code != http.StatusOK {
		t.Fatalf("first PATCH status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec := f.doPatch(t, f.domain, accessToken, fmt.Sprintf(`{"name": "Ada Lovelace", "avatar_id": %q}`, newFileID)); rec.Code != http.StatusOK {
		t.Fatalf("second PATCH status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	oldFile, err := f.filesStore.GetByID(context.Background(), f.tenantSlug, oldFileID)
	if err != nil {
		t.Fatalf("GetByID(old) error: %v", err)
	}
	if oldFile.DeletedAt == nil {
		t.Error("old avatar file's DeletedAt is nil, want it marked deleted after being replaced")
	}

	newFile, err := f.filesStore.GetByID(context.Background(), f.tenantSlug, newFileID)
	if err != nil {
		t.Fatalf("GetByID(new) error: %v", err)
	}
	if newFile.DeletedAt != nil {
		t.Error("new avatar file's DeletedAt is set, want it untouched")
	}
}

func TestServeHTTP_EmptyStringAvatarIDClearsAvatarAndMarksOldFileDeleted(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)
	fileID := f.insertFile(t, "avatars")

	if rec := f.doPatch(t, f.domain, accessToken, fmt.Sprintf(`{"name": "Ada Lovelace", "avatar_id": %q}`, fileID)); rec.Code != http.StatusOK {
		t.Fatalf("first PATCH status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec := f.doPatch(t, f.domain, accessToken, `{"name": "Ada Lovelace", "avatar_id": ""}`); rec.Code != http.StatusOK {
		t.Fatalf("second PATCH status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	profile, err := f.userStore.GetProfile(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.AvatarFileID != nil {
		t.Errorf("stored AvatarFileID = %v, want nil (an empty-string avatar_id must clear it)", *profile.AvatarFileID)
	}

	file, err := f.filesStore.GetByID(context.Background(), f.tenantSlug, fileID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if file.DeletedAt == nil {
		t.Error("cleared avatar file's DeletedAt is nil, want it marked deleted")
	}
}

func TestServeHTTP_AbsentAvatarIDLeavesExistingAvatarUntouched(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)
	fileID := f.insertFile(t, "avatars")

	if rec := f.doPatch(t, f.domain, accessToken, fmt.Sprintf(`{"name": "First", "avatar_id": %q}`, fileID)); rec.Code != http.StatusOK {
		t.Fatalf("first PATCH status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec := f.doPatch(t, f.domain, accessToken, `{"name": "Second"}`); rec.Code != http.StatusOK {
		t.Fatalf("second PATCH status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	profile, err := f.userStore.GetProfile(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if profile.AvatarFileID == nil || *profile.AvatarFileID != fileID {
		t.Errorf("stored AvatarFileID = %v, want %q (an absent avatar_id must not touch it)", profile.AvatarFileID, fileID)
	}

	file, err := f.filesStore.GetByID(context.Background(), f.tenantSlug, fileID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if file.DeletedAt != nil {
		t.Error("untouched avatar file's DeletedAt is set, want it still live")
	}
}

func TestServeHTTP_BlankNameRejected(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)

	rec := f.doPatch(t, f.domain, accessToken, `{"name": "   "}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestServeHTTP_NoTokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doPatch(t, f.domain, "", `{"name": "Ada Lovelace"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestServeHTTP_MalformedBodyRejected(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)

	rec := f.doPatch(t, f.domain, accessToken, `{not json`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
