package apikey

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// testEnv wires an apikey.Store plus tenant.Store/user.Store against the
// real compose.dev.yml Postgres — api_keys FK-references both tenants and
// users, so tests need real rows in each.
type testEnv struct {
	store       *Store
	tenantStore *tenant.Store
	userStore   *user.Store
	conn        *sql.DB
}

func openTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}

	store := NewStore(conn)
	if err := store.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return &testEnv{store: store, tenantStore: tenantStore, userStore: userStore, conn: conn}
}

func uniqueName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("apikeytest%d", time.Now().UnixNano())
}

// createTenant creates a tenant, fails the test on error, and registers
// its scoped cleanup — api_keys cascades on delete, so no separate
// cleanup is needed for key rows.
func (e *testEnv) createTenant(t *testing.T) *tenant.Tenant {
	t.Helper()
	slug := uniqueName(t)
	tt, err := e.tenantStore.CreateTenant(context.Background(), slug, "API Key Test Co")
	if err != nil {
		t.Fatalf("CreateTenant(%q) error: %v", slug, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })
	return tt
}

// createUser creates a user, fails the test on error, and registers its
// scoped cleanup.
func (e *testEnv) createUser(t *testing.T) string {
	t.Helper()
	email := uniqueName(t) + "@example.com"
	userID, err := e.userStore.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited(%q) error: %v", email, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.users WHERE id = $1", userID) })
	return userID
}

func TestBootstrap_CreatesTableAndIndex(t *testing.T) {
	env := openTestEnv(t)

	var tableExists bool
	err := env.conn.QueryRowContext(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'system' AND table_name = 'api_keys'
		)
	`).Scan(&tableExists)
	if err != nil {
		t.Fatalf("check table exists: %v", err)
	}
	if !tableExists {
		t.Error("expected system.api_keys to exist after Bootstrap()")
	}

	var indexDef string
	err = env.conn.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'idx_api_keys_hash'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected idx_api_keys_hash to exist: %v", err)
	}
	if indexDef == "" {
		t.Fatal("expected a non-empty index definition")
	}
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	env := openTestEnv(t)

	if err := env.store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

// TestBootstrap_ConcurrentCallsAllSucceed guards against goerp#171 — see
// tenant.TestBootstrap_ConcurrentCallsAllSucceed's doc comment for what
// this does and doesn't prove.
func TestBootstrap_ConcurrentCallsAllSucceed(t *testing.T) {
	env := openTestEnv(t)

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			errs <- env.store.Bootstrap(context.Background())
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Bootstrap() error: %v", err)
		}
	}
}

func TestIssueKey_ReturnsPlaintextOnceAndStoresOnlyHash(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	userID := env.createUser(t)

	fullKey, k, err := env.store.IssueKey(context.Background(), tt.ID, &userID, "Test Key", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	// Parse by fixed offset, not by splitting on "_" — base64.RawURLEncoding
	// legitimately emits "_" as part of its own alphabet, so the random
	// body can itself contain additional underscores.
	if !strings.HasPrefix(fullKey, "erp_") {
		t.Errorf("fullKey = %q, want erp_ prefix", fullKey)
	}
	const prefixStart, prefixLen = len("erp_"), 8
	if len(fullKey) < prefixStart+prefixLen+1 {
		t.Fatalf("fullKey = %q, too short for erp_{prefix8}_{random32} shape", fullKey)
	}
	prefix := fullKey[prefixStart : prefixStart+prefixLen]
	if fullKey[prefixStart+prefixLen] != '_' {
		t.Errorf("fullKey = %q, want a \"_\" separator right after the 8-char prefix", fullKey)
	}
	body := fullKey[prefixStart+prefixLen+1:]
	if len(body) != 32 {
		t.Errorf("random segment = %q (len %d), want 32 chars", body, len(body))
	}
	if prefix != k.KeyPrefix {
		t.Errorf("KeyPrefix = %q, want %q (the prefix segment of the returned key)", k.KeyPrefix, prefix)
	}

	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(fullKey)))
	if k.KeyHash != wantHash {
		t.Errorf("KeyHash = %q, want %q", k.KeyHash, wantHash)
	}

	var storedHash string
	if err := env.conn.QueryRowContext(context.Background(), "SELECT key_hash FROM system.api_keys WHERE id = $1", k.ID).Scan(&storedHash); err != nil {
		t.Fatalf("query stored key_hash: %v", err)
	}
	if storedHash != wantHash {
		t.Errorf("stored key_hash = %q, want %q — the database must never store the plaintext key", storedHash, wantHash)
	}
}

func TestIssueKey_ServiceKeyHasNilUserID(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	_, k, err := env.store.IssueKey(context.Background(), tt.ID, nil, "Service Key", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}
	if k.UserID != nil {
		t.Errorf("UserID = %v, want nil for a service key", *k.UserID)
	}
}

func TestIssueKey_ScopesAndAllowedIPsRoundTrip(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	scopes := []string{"sales:order:read", "sales:order:write"}
	allowedIPs := []string{"10.0.0.1", "192.168.1.0/24"}

	fullKey, k, err := env.store.IssueKey(context.Background(), tt.ID, nil, "Scoped Key", scopes, allowedIPs, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}
	if len(k.Scopes) != 2 || k.Scopes[0] != "sales:order:read" || k.Scopes[1] != "sales:order:write" {
		t.Errorf("Scopes = %v, want %v", k.Scopes, scopes)
	}
	if len(k.AllowedIPs) != 2 {
		t.Fatalf("AllowedIPs = %v, want 2 entries", k.AllowedIPs)
	}

	got, err := env.store.LookupByHash(context.Background(), fullKey)
	if err != nil {
		t.Fatalf("LookupByHash() error: %v", err)
	}
	if len(got.Scopes) != 2 || len(got.AllowedIPs) != 2 {
		t.Errorf("LookupByHash() Scopes/AllowedIPs = %v/%v, want the same round-tripped values", got.Scopes, got.AllowedIPs)
	}
}

func TestIssueKey_UnknownTenantFails(t *testing.T) {
	env := openTestEnv(t)

	_, _, err := env.store.IssueKey(context.Background(), "00000000-0000-0000-0000-000000000000", nil, "Bad Tenant", nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected a foreign key violation for an unknown tenant")
	}
}

func TestLookupByHash_FindsIssuedKey(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	fullKey, k, err := env.store.IssueKey(context.Background(), tt.ID, nil, "Lookup Me", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	got, err := env.store.LookupByHash(context.Background(), fullKey)
	if err != nil {
		t.Fatalf("LookupByHash() error: %v", err)
	}
	if got.ID != k.ID {
		t.Errorf("ID = %q, want %q", got.ID, k.ID)
	}
}

func TestLookupByHash_UnknownKeyReturnsErrAPIKeyNotFound(t *testing.T) {
	env := openTestEnv(t)

	_, err := env.store.LookupByHash(context.Background(), "erp_notreal_notreal")
	if !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("LookupByHash() error = %v, want ErrAPIKeyNotFound", err)
	}
}

func TestLookupByHash_RevokedKeyReturnsErrAPIKeyNotFound(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	fullKey, k, err := env.store.IssueKey(context.Background(), tt.ID, nil, "Revoke Me", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}
	if err := env.store.Revoke(context.Background(), k.ID, "test"); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	_, err = env.store.LookupByHash(context.Background(), fullKey)
	if !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("LookupByHash() error = %v, want ErrAPIKeyNotFound", err)
	}
}

func TestLookupByHash_ExpiredKeyStillFound(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	past := time.Now().Add(-time.Hour)

	fullKey, _, err := env.store.IssueKey(context.Background(), tt.ID, nil, "Expired Key", nil, nil, &past, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	got, err := env.store.LookupByHash(context.Background(), fullKey)
	if err != nil {
		t.Fatalf("LookupByHash() error: %v, want no error — expiry is a distinct check the caller makes, not LookupByHash's own filter", err)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Before(time.Now()) {
		t.Errorf("ExpiresAt = %v, want a past timestamp", got.ExpiresAt)
	}
}

func TestRevoke_SetsRevokedAtAndReason(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	_, k, err := env.store.IssueKey(context.Background(), tt.ID, nil, "To Revoke", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}
	if err := env.store.Revoke(context.Background(), k.ID, "no longer needed"); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	var revokedAt sql.NullTime
	var reason sql.NullString
	if err := env.conn.QueryRowContext(context.Background(), "SELECT revoked_at, revoke_reason FROM system.api_keys WHERE id = $1", k.ID).Scan(&revokedAt, &reason); err != nil {
		t.Fatalf("query revoked row: %v", err)
	}
	if !revokedAt.Valid {
		t.Error("revoked_at is NULL, want set")
	}
	if reason.String != "no longer needed" {
		t.Errorf("revoke_reason = %q, want %q", reason.String, "no longer needed")
	}
}

func TestUpdateLastUsed_SetsTimestampAndIP(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	_, k, err := env.store.IssueKey(context.Background(), tt.ID, nil, "Track Usage", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}
	if err := env.store.UpdateLastUsed(context.Background(), k.ID, "203.0.113.7"); err != nil {
		t.Fatalf("UpdateLastUsed() error: %v", err)
	}

	var lastUsedAt sql.NullTime
	var lastUsedIP sql.NullString
	if err := env.conn.QueryRowContext(context.Background(), "SELECT last_used_at, last_used_ip FROM system.api_keys WHERE id = $1", k.ID).Scan(&lastUsedAt, &lastUsedIP); err != nil {
		t.Fatalf("query last_used row: %v", err)
	}
	if !lastUsedAt.Valid {
		t.Error("last_used_at is NULL, want set")
	}
	if lastUsedIP.String != "203.0.113.7" {
		t.Errorf("last_used_ip = %q, want %q", lastUsedIP.String, "203.0.113.7")
	}
}

func TestEncodePGTextArray_EmptySliceProducesEmptyLiteral(t *testing.T) {
	if got := encodePGTextArray(nil); got != "{}" {
		t.Errorf("encodePGTextArray(nil) = %q, want {}", got)
	}
	if got := encodePGTextArray([]string{}); got != "{}" {
		t.Errorf("encodePGTextArray([]string{}) = %q, want {}", got)
	}
}

func TestEncodePGTextArray_QuotesAndEscapesEachElement(t *testing.T) {
	got := encodePGTextArray([]string{"a", `has "quotes"`, `has\backslash`})
	want := `{"a","has \"quotes\"","has\\backslash"}`
	if got != want {
		t.Errorf("encodePGTextArray(...) = %q, want %q", got, want)
	}
}

func TestDecodePGTextArray_RoundTripsThroughEncode(t *testing.T) {
	cases := [][]string{
		{},
		{"a"},
		{"a", "b", "c"},
		{"sales:order:read", "sales:order:write"},
		{"10.0.0.1", "192.168.1.0/24"},
		{`has "quotes"`, `has,comma`, `has\backslash`},
	}
	for _, elems := range cases {
		encoded := encodePGTextArray(elems)
		got := decodePGTextArray(encoded)
		if len(got) != len(elems) {
			t.Errorf("decodePGTextArray(encodePGTextArray(%v)) = %v, want %v", elems, got, elems)
			continue
		}
		for i := range elems {
			if got[i] != elems[i] {
				t.Errorf("decodePGTextArray(encodePGTextArray(%v))[%d] = %q, want %q", elems, i, got[i], elems[i])
			}
		}
	}
}

func TestDecodePGTextArray_HandlesUnquotedElements(t *testing.T) {
	// Postgres's own text-cast output only quotes an element when it
	// needs to — decode must handle both forms.
	got := decodePGTextArray(`{a,b,"c d"}`)
	want := []string{"a", "b", "c d"}
	if len(got) != len(want) {
		t.Fatalf("decodePGTextArray(...) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("decodePGTextArray(...)[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDecodePGTextArray_EmptyLiteralProducesEmptySlice(t *testing.T) {
	got := decodePGTextArray("{}")
	if len(got) != 0 {
		t.Errorf("decodePGTextArray(\"{}\") = %v, want empty", got)
	}
}
