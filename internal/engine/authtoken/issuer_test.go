package authtoken

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/session"
	"github.com/djangbahevans/goerp/internal/engine/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/role's and
// internal/engine/tenant's tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// fixture is one login's worth of real, FK-satisfying rows: a tenant, a
// user, and a provisioned tenant schema with one role granted. Cleaned up
// by exact row id, never a blanket DELETE — system.tenants/system.users
// are real shared tables other packages' tests race against concurrently.
type fixture struct {
	issuer     *Issuer
	tenantSlug string
	userID     string
	sessions   *session.Store
	conn       *sql.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockSigningKeyTable(t, conn)

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}
	sessionStore := session.NewStore(conn)
	if err := sessionStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("session Bootstrap() error: %v", err)
	}
	signingKeyStore := signingkey.NewStore(conn, &secrets.EnvBackend{})
	if err := signingKeyStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("signingkey Bootstrap() error: %v", err)
	}
	keySet, err := signingKeyStore.LoadOrGenerate(context.Background())
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}

	slug := fmt.Sprintf("authtokentest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(context.Background(), slug, "Auth Token Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID)
	})

	userID, err := userStore.FindOrCreateInvited(context.Background(), slug+"@example.com")
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID)
	})

	schema := tenantschema.Name(slug)
	if _, err := conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
	})

	roleStore := role.NewStore(conn)
	if err := roleStore.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := roleStore.SeedBuiltinRoles(context.Background(), slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := roleStore.GetRoleByName(context.Background(), slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	if _, err := conn.Exec(fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), userID, roleID); err != nil {
		t.Fatalf("grant admin role: %v", err)
	}

	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID)
	})

	return &fixture{
		issuer:     NewIssuer(&keySet.Active, tenantStore, roleStore, sessionStore),
		tenantSlug: slug,
		userID:     userID,
		sessions:   sessionStore,
		conn:       conn,
	}
}

// lockSigningKeyTable takes a session-scoped Postgres advisory lock
// (pg_advisory_lock, explicitly released at test cleanup) — a key
// distinct from the one LoadOrGenerate itself locks internally
// (db.WithAdvisoryLock's transaction-scoped pg_advisory_xact_lock), since
// holding that same key here would deadlock this test's own later
// LoadOrGenerate call against itself (a different pooled connection
// blocking on a lock this test's own session already holds). This
// serializes the test against every other package's test touching the
// shared system.jwt_signing_keys table instead: signingkey, authcheck,
// and authtoken tests all exercise its single-active-row constraint
// against the same real compose.dev.yml Postgres instance; without this,
// one package's in-flight active row is visible mid-test to another
// package's concurrently running test, whose own (different,
// process-local) secrets backend has no way to load that row's private
// key material — "parse private key material ...: no PEM block found".
// Safe here specifically because localPostgresDSN bypasses PgBouncer —
// a session-scoped lock isn't safe under PgBouncer's transaction pooling
// (see db.WithAdvisoryLock's doc comment for why production code uses a
// transaction-scoped lock instead).
func lockSigningKeyTable(t *testing.T, pool *sql.DB) {
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
		// sql.Conn.Close returns the connection to the pool for reuse
		// rather than necessarily terminating the physical session, so it
		// does not by itself release a session-scoped advisory lock —
		// unlock explicitly first, or the next test wanting this lock
		// hangs forever waiting on one nothing will ever release.
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		_ = conn.Close()
	})
}

func TestIssue_AccessTokenClaimsMatchDocumentedShape(t *testing.T) {
	f := newFixture(t)

	tokens, err := f.issuer.Issue(context.Background(), LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	pub := f.issuer.signingKey.Public
	parsed, err := jwt.ParseWithClaims(tokens.AccessToken, &Claims{}, func(tok *jwt.Token) (any, error) {
		return pub, nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("ParseWithClaims() error: %v, valid: %v", err, parsed != nil && parsed.Valid)
	}
	claims := parsed.Claims.(*Claims)

	tt, err := f.issuer.tenants.GetBySlug(context.Background(), f.tenantSlug)
	if err != nil {
		t.Fatalf("GetBySlug() error: %v", err)
	}

	if claims.Issuer != "goerp" {
		t.Errorf("iss = %q, want goerp", claims.Issuer)
	}
	if claims.Subject != f.userID {
		t.Errorf("sub = %q, want %q", claims.Subject, f.userID)
	}
	if claims.TenantID != tt.ID {
		t.Errorf("tid = %q, want tenant UUID %q", claims.TenantID, tt.ID)
	}
	if claims.TenantID == f.tenantSlug {
		t.Error("tid equals the tenant slug — must always be the UUID, never the slug")
	}
	if claims.SessionID == "" {
		t.Error("sid is empty")
	}
	if claims.ID == "" {
		t.Error("jti is empty")
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "admin" {
		t.Errorf("roles = %v, want [admin]", claims.Roles)
	}
	if len(claims.Scope) != 1 || claims.Scope[0] != "api" {
		t.Errorf("scp = %v, want [api]", claims.Scope)
	}
	if len(claims.AMR) != 1 || claims.AMR[0] != "pwd" {
		t.Errorf("amr = %v, want [pwd]", claims.AMR)
	}
	if claims.MFAVerifiedAt != nil {
		t.Errorf("mfa_verified_at = %v, want nil", claims.MFAVerifiedAt)
	}
	if got := claims.ExpiresAt.Sub(claims.IssuedAt.Time); got != 15*time.Minute {
		t.Errorf("exp - iat = %v, want 15m", got)
	}
}

func TestIssue_RefreshTokenStoredOnlyAsHash(t *testing.T) {
	f := newFixture(t)

	tokens, err := f.issuer.Issue(context.Background(), LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	raw, err := base64.RawURLEncoding.DecodeString(tokens.RefreshToken)
	if err != nil {
		t.Fatalf("refresh token isn't valid base64url: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("decoded refresh token length = %d bytes, want 32", len(raw))
	}

	wantHash := sha256.Sum256([]byte(tokens.RefreshToken))
	var gotHash string
	err = f.conn.QueryRow(`SELECT refresh_hash FROM system.sessions WHERE user_id = $1`, f.userID).Scan(&gotHash)
	if err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if gotHash != hex.EncodeToString(wantHash[:]) {
		t.Error("stored refresh_hash doesn't match sha256(refresh token)")
	}

	var rawTokenStoredSomewhere bool
	err = f.conn.QueryRow(`SELECT EXISTS(SELECT 1 FROM system.sessions WHERE refresh_hash = $1)`, tokens.RefreshToken).Scan(&rawTokenStoredSomewhere)
	if err != nil {
		t.Fatalf("check for plaintext token: %v", err)
	}
	if rawTokenStoredSomewhere {
		t.Error("refresh_hash column contains the plaintext refresh token, not its hash")
	}
}

func TestIssue_SessionRowIsItsOwnFamily(t *testing.T) {
	f := newFixture(t)

	tokens, err := f.issuer.Issue(context.Background(), LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	_ = tokens

	var id, familyID string
	err = f.conn.QueryRow(`SELECT id, family_id FROM system.sessions WHERE user_id = $1`, f.userID).Scan(&id, &familyID)
	if err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if id != familyID {
		t.Errorf("id = %q, family_id = %q, want equal for a fresh login", id, familyID)
	}
}

func TestIssue_GeneratesDeviceIDWhenNotSupplied(t *testing.T) {
	f := newFixture(t)

	_, err := f.issuer.Issue(context.Background(), LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	var deviceID string
	err = f.conn.QueryRow(`SELECT device_id FROM system.sessions WHERE user_id = $1`, f.userID).Scan(&deviceID)
	if err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if deviceID == "" {
		t.Error("device_id is empty, want a generated UUID")
	}
}
