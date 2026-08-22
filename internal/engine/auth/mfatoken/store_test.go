package mfatoken

import (
	"bytes"
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance (bypassing PgBouncer), same convention as
// signingkey/rowcrypt's own tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// memoryBackend is an in-process secrets.Backend that actually supports
// Set, standing in for a real vault/aws_secretsmanager deployment — same
// helper rowcrypt's tests use.
type memoryBackend struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{values: make(map[string]string)}
}

func (b *memoryBackend) Get(ctx context.Context, key string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.values[key], nil
}

func (b *memoryBackend) Set(ctx context.Context, key, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.values[key] = value
	return nil
}

func (b *memoryBackend) Rotate(ctx context.Context, key string) (string, error) {
	return "", secrets.ErrRotateNotSupported
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockMFATokenSigningKeysTable(t, conn)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.mfa_token_signing_keys`)
	})

	return conn
}

// lockMFATokenSigningKeysTable mirrors rowcrypt.lockRowEncryptionKeysTable
// — serializes every test in this package against the shared
// system.mfa_token_signing_keys table's single-active-row constraint.
func lockMFATokenSigningKeysTable(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	key := db.AdvisoryLockKey("test.mfa_token_signing_keys_table")

	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire dedicated connection for mfa-token-key lock: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatalf("acquire mfa-token-key advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		_ = conn.Close()
	})
}

func openTestStore(t *testing.T, secretsBackend secrets.Backend) *Store {
	t.Helper()

	conn := openTestDB(t)
	store := NewStore(conn, secretsBackend)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return store
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store := openTestStore(t, newMemoryBackend())

	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

func TestBootstrap_CreatesActiveUniqueIndex(t *testing.T) {
	conn := openTestDB(t)
	store := NewStore(conn, newMemoryBackend())
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	var indexDef string
	err := conn.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'mfa_token_signing_keys_active_unique_idx'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected mfa_token_signing_keys_active_unique_idx to exist: %v", err)
	}
	if indexDef == "" {
		t.Fatal("expected a non-empty index definition")
	}
}

func TestLoadOrGenerate_WithPersistentBackend_GeneratesOnceAndReloadsSameKey(t *testing.T) {
	backend := newMemoryBackend()
	store := openTestStore(t, backend)
	ctx := context.Background()

	first, err := store.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("first LoadOrGenerate() error: %v", err)
	}
	if first.Active.KeyID == "" {
		t.Fatal("expected a non-empty KeyID")
	}
	if len(first.Active.Secret) != 32 {
		t.Errorf("Secret length = %d, want 32 (HMAC-SHA256)", len(first.Active.Secret))
	}
	if first.Active.SecretManagerVersion != "1" {
		t.Errorf("SecretManagerVersion = %q, want \"1\" for a freshly persisted key", first.Active.SecretManagerVersion)
	}
	if len(first.Previous) != 0 {
		t.Errorf("Previous = %v, want empty — rotation isn't implemented", first.Previous)
	}

	second, err := store.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("second LoadOrGenerate() error: %v", err)
	}
	if second.Active.KeyID != first.Active.KeyID {
		t.Errorf("second LoadOrGenerate() KeyID = %q, want the same as the first (%q) — should load, not regenerate", second.Active.KeyID, first.Active.KeyID)
	}
	if !bytes.Equal(second.Active.Secret, first.Active.Secret) {
		t.Error("second LoadOrGenerate() loaded different key material than the first generated")
	}
}

// TestLoadOrGenerate_EnvBackendIsEphemeral guards the dev-mode fallback:
// secrets.EnvBackend.Set always returns secrets.ErrSetNotSupported, so a
// key generated against it must still be usable this process — just never
// persisted, meaning a second call regenerates rather than reloading.
func TestLoadOrGenerate_EnvBackendIsEphemeral(t *testing.T) {
	store := openTestStore(t, &secrets.EnvBackend{})
	ctx := context.Background()

	first, err := store.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("first LoadOrGenerate() error: %v", err)
	}
	if first.Active.SecretManagerVersion != "ephemeral" {
		t.Errorf("SecretManagerVersion = %q, want \"ephemeral\" for a Set-unsupported backend", first.Active.SecretManagerVersion)
	}

	var rowCount int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM system.mfa_token_signing_keys`).Scan(&rowCount); err != nil {
		t.Fatalf("count mfa_token_signing_keys rows: %v", err)
	}
	if rowCount != 0 {
		t.Errorf("mfa_token_signing_keys row count = %d, want 0 — an ephemeral key must not be persisted", rowCount)
	}

	second, err := store.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("second LoadOrGenerate() error: %v", err)
	}
	if second.Active.KeyID == first.Active.KeyID {
		t.Error("second LoadOrGenerate() returned the same KeyID as the first — an ephemeral key should regenerate every call, not persist")
	}
}

func TestLoadOrGenerate_KeyIssuesAndVerifiesTokens(t *testing.T) {
	store := openTestStore(t, newMemoryBackend())
	set, err := store.LoadOrGenerate(context.Background())
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}

	codec := NewCodec(&set.Active)
	token, _, err := codec.Issue("user-1", "tenant-1", "https://acmecorp.goerp.io")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	if _, err := codec.Verify(token); err != nil {
		t.Errorf("Verify() of a token issued against the loaded key error: %v", err)
	}
}
