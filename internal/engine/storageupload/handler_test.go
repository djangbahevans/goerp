package storageupload

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"mime/multipart"
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
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// fixture mirrors authme's own fixture shape, plus a files.Store and a
// real local storage.Backend.
type fixture struct {
	handler    *Handler
	issuer     *authtoken.Issuer
	filesStore *files.Store
	domain     string
	tenantSlug string
	userID     string
	conn       *sql.DB
}

func newFixture(t *testing.T, limits Limits) *fixture {
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

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New() error: %v", err)
	}

	filesStore := files.NewStore(conn)

	handler := NewHandler(tenantResolver, authChecker, backend, filesStore, limits)

	slug := fmt.Sprintf("storageuploadtest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Storage Upload Test Co")
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
		handler:    handler,
		issuer:     issuer,
		filesStore: filesStore,
		domain:     domain,
		tenantSlug: slug,
		userID:     userID,
		conn:       conn,
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

// multipartBody builds a request body for POST /storage/upload. purpose
// is omitted entirely when empty (exercising the default), rather than
// sent as an empty string.
func multipartBody(t *testing.T, filename, contentType string, data []byte, purpose string) (body *bytes.Buffer, boundary string) {
	t.Helper()
	body = &bytes.Buffer{}
	w := multipart.NewWriter(body)

	part, err := w.CreatePart(map[string][]string{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename=%q`, filename)},
		"Content-Type":        {contentType},
	})
	if err != nil {
		t.Fatalf("CreatePart(file) error: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write file part: %v", err)
	}

	if purpose != "" {
		if err := w.WriteField("purpose", purpose); err != nil {
			t.Fatalf("WriteField(purpose) error: %v", err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body, w.Boundary()
}

func (f *fixture) doUpload(t *testing.T, host, accessToken string, body *bytes.Buffer, boundary string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/storage/upload", body)
	req.Host = host
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func defaultLimits() Limits {
	return Limits{MaxFileBytes: 1 << 20, AllowedTypes: nil, BlockedTypes: nil}
}

func TestServeHTTP_UploadsFileAndReturnsFileID(t *testing.T) {
	f := newFixture(t, defaultLimits())
	accessToken := f.issueAccessToken(t)

	data := []byte("hello world")
	body, boundary := multipartBody(t, "hello.txt", "text/plain", data, "")
	rec := f.doUpload(t, f.domain, accessToken, body, boundary)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp uploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.FileID == "" {
		t.Error("file_id is empty")
	}
	if resp.Name != "hello.txt" {
		t.Errorf("name = %q, want %q", resp.Name, "hello.txt")
	}
	if resp.ContentType != "text/plain" {
		t.Errorf("content_type = %q, want %q", resp.ContentType, "text/plain")
	}
	if resp.SizeBytes != int64(len(data)) {
		t.Errorf("size_bytes = %d, want %d", resp.SizeBytes, len(data))
	}

	var storedPurpose, storedName string
	if err := f.conn.QueryRow(
		fmt.Sprintf("SELECT purpose, original_name FROM %s.files WHERE id = $1", tenantschema.Name(f.tenantSlug)),
		resp.FileID,
	).Scan(&storedPurpose, &storedName); err != nil {
		t.Fatalf("query files row: %v", err)
	}
	if storedPurpose != "attachments" {
		t.Errorf("stored purpose = %q, want default %q", storedPurpose, "attachments")
	}
	if storedName != "hello.txt" {
		t.Errorf("stored original_name = %q, want %q", storedName, "hello.txt")
	}
}

func TestServeHTTP_HonorsExplicitPurpose(t *testing.T) {
	f := newFixture(t, defaultLimits())
	accessToken := f.issueAccessToken(t)

	body, boundary := multipartBody(t, "avatar.png", "image/png", []byte("fake-png-bytes"), "avatars")
	rec := f.doUpload(t, f.domain, accessToken, body, boundary)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp uploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	var storedPurpose string
	if err := f.conn.QueryRow(
		fmt.Sprintf("SELECT purpose FROM %s.files WHERE id = $1", tenantschema.Name(f.tenantSlug)),
		resp.FileID,
	).Scan(&storedPurpose); err != nil {
		t.Fatalf("query files row: %v", err)
	}
	if storedPurpose != "avatars" {
		t.Errorf("stored purpose = %q, want %q", storedPurpose, "avatars")
	}
}

func TestServeHTTP_NoTokenRejected(t *testing.T) {
	f := newFixture(t, defaultLimits())

	body, boundary := multipartBody(t, "hello.txt", "text/plain", []byte("hi"), "")
	rec := f.doUpload(t, f.domain, "", body, boundary)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestServeHTTP_OversizedFileRejectedWithoutWritingFilesRow(t *testing.T) {
	f := newFixture(t, Limits{MaxFileBytes: 10})
	accessToken := f.issueAccessToken(t)

	body, boundary := multipartBody(t, "big.txt", "text/plain", bytes.Repeat([]byte("a"), 1000), "")
	rec := f.doUpload(t, f.domain, accessToken, body, boundary)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := f.conn.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s.files", tenantschema.Name(f.tenantSlug))).Scan(&count); err != nil {
		t.Fatalf("count files rows: %v", err)
	}
	if count != 0 {
		t.Errorf("files row count = %d, want 0 (oversized upload must not write one)", count)
	}
}

func TestServeHTTP_DisallowedContentTypeRejectedWithoutWritingFilesRow(t *testing.T) {
	f := newFixture(t, Limits{MaxFileBytes: 1 << 20, AllowedTypes: []string{"image/png"}})
	accessToken := f.issueAccessToken(t)

	body, boundary := multipartBody(t, "evil.sh", "application/x-sh", []byte("#!/bin/sh"), "")
	rec := f.doUpload(t, f.domain, accessToken, body, boundary)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415; body = %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := f.conn.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s.files", tenantschema.Name(f.tenantSlug))).Scan(&count); err != nil {
		t.Fatalf("count files rows: %v", err)
	}
	if count != 0 {
		t.Errorf("files row count = %d, want 0 (disallowed content type must not write one)", count)
	}
}

func TestServeHTTP_MissingFileFieldRejected(t *testing.T) {
	f := newFixture(t, defaultLimits())
	accessToken := f.issueAccessToken(t)

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	if err := w.WriteField("purpose", "attachments"); err != nil {
		t.Fatalf("WriteField() error: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := f.doUpload(t, f.domain, accessToken, body, w.Boundary())
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestServeHTTP_NilBackendReturns503(t *testing.T) {
	f := newFixture(t, defaultLimits())
	f.handler.backend = nil
	accessToken := f.issueAccessToken(t)

	body, boundary := multipartBody(t, "hello.txt", "text/plain", []byte("hi"), "")
	rec := f.doUpload(t, f.domain, accessToken, body, boundary)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
