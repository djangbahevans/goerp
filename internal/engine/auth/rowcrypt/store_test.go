package rowcrypt

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance (bypassing PgBouncer), same convention as signingkey.Store's
// tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// memoryBackend is an in-process secrets.Backend that actually supports
// Set, standing in for a real vault/aws_secretsmanager deployment — neither
// is part of compose.dev.yml. It exercises this package's own persist/load
// logic; the database side of every test still runs against real Postgres.
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
	lockRowEncryptionKeysTable(t, conn)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.row_encryption_keys`)
	})

	return conn
}

// lockRowEncryptionKeysTable takes a session-scoped Postgres advisory lock
// (pg_advisory_lock, explicitly released at test cleanup) — a key distinct
// from the one LoadOrGenerate itself locks internally
// (db.WithAdvisoryLock's transaction-scoped pg_advisory_xact_lock), since
// holding that same key here would deadlock this test's own later
// LoadOrGenerate call against itself. Serializes every test in this
// package against the shared system.row_encryption_keys table's
// single-active-row constraint, the same reasoning
// signingkey.lockSigningKeyTable documents for system.jwt_signing_keys.
// Safe here specifically because localPostgresDSN bypasses PgBouncer.
func lockRowEncryptionKeysTable(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	key := db.AdvisoryLockKey("test.row_encryption_keys_table")

	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire dedicated connection for row-encryption-key lock: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatalf("acquire row-encryption-key advisory lock: %v", err)
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

// TestBootstrap_ConcurrentCallsAllSucceed guards against goerp#171 — see
// tenant.TestBootstrap_ConcurrentCallsAllSucceed's doc comment for what
// this does and doesn't prove.
func TestBootstrap_ConcurrentCallsAllSucceed(t *testing.T) {
	store := openTestStore(t, newMemoryBackend())

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			errs <- store.Bootstrap(context.Background())
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

func TestBootstrap_CreatesActiveUniqueIndex(t *testing.T) {
	conn := openTestDB(t)
	store := NewStore(conn, newMemoryBackend())
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	var indexDef string
	err := conn.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'row_encryption_keys_active_unique_idx'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected row_encryption_keys_active_unique_idx to exist: %v", err)
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
	if len(first.Active.Key) != 32 {
		t.Errorf("Key length = %d, want 32 (AES-256)", len(first.Active.Key))
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
	if !bytes.Equal(second.Active.Key, first.Active.Key) {
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
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM system.row_encryption_keys`).Scan(&rowCount); err != nil {
		t.Fatalf("count row_encryption_keys rows: %v", err)
	}
	if rowCount != 0 {
		t.Errorf("row_encryption_keys row count = %d, want 0 — an ephemeral key must not be persisted", rowCount)
	}

	second, err := store.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("second LoadOrGenerate() error: %v", err)
	}
	if second.Active.KeyID == first.Active.KeyID {
		t.Error("second LoadOrGenerate() returned the same KeyID as the first — an ephemeral key should regenerate every call, not persist")
	}
}

func TestEncryptDecrypt_RoundTripsWithActiveKey(t *testing.T) {
	store := openTestStore(t, newMemoryBackend())
	set, err := store.LoadOrGenerate(context.Background())
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}

	plaintext := []byte("JBSWY3DPEHPK3PXP") // a fake TOTP secret
	ciphertext, err := set.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}

	got, err := set.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() error: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("Decrypt() = %q, want %q", got, plaintext)
	}
}

func TestEncrypt_ProducesKeyIDNonceCiphertextFormat(t *testing.T) {
	store := openTestStore(t, newMemoryBackend())
	set, err := store.LoadOrGenerate(context.Background())
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}

	ciphertext, err := set.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}

	parts := strings.SplitN(string(ciphertext), ":", 3)
	if len(parts) != 3 {
		t.Fatalf("ciphertext = %q, want exactly 3 colon-delimited segments", ciphertext)
	}
	if parts[0] != set.Active.KeyID {
		t.Errorf("first segment = %q, want the active key id %q", parts[0], set.Active.KeyID)
	}
	if parts[1] == "" || parts[2] == "" {
		t.Errorf("nonce/ciphertext segments = %q/%q, want non-empty", parts[1], parts[2])
	}
}

func TestDecrypt_PreviousKeyStillDecryptsAfterRotation(t *testing.T) {
	store := openTestStore(t, newMemoryBackend())
	ctx := context.Background()

	// Simulate what a future rotation job would do: encrypt under today's
	// Active key, then hand-build a RowKeySet where that same key has
	// moved to Previous and a different key is Active — the shape
	// LoadOrGenerate would return after a real rotation, which nothing in
	// this package can perform yet.
	before, err := store.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}
	plaintext := []byte("recovery-code-plaintext")
	ciphertext, err := before.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}

	rotated := RowKeySet{
		Active: RowKey{KeyID: "new-active-key", Key: bytes.Repeat([]byte{0x42}, 32)},
		Previous: []RowKey{
			before.Active,
		},
	}

	got, err := rotated.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() with rotated key set error: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("Decrypt() = %q, want %q", got, plaintext)
	}
}

func TestDecrypt_UnknownKeyIDFails(t *testing.T) {
	set := RowKeySet{Active: RowKey{KeyID: "k1", Key: bytes.Repeat([]byte{0x01}, 32)}}

	_, err := set.Decrypt([]byte("unknown-key-id:AAAA:BBBB"))
	if !errors.Is(err, ErrUnknownKeyID) {
		t.Errorf("Decrypt() error = %v, want ErrUnknownKeyID", err)
	}
}

func TestDecrypt_MalformedCiphertextFails(t *testing.T) {
	set := RowKeySet{Active: RowKey{KeyID: "k1", Key: bytes.Repeat([]byte{0x01}, 32)}}

	_, err := set.Decrypt([]byte("not-enough-segments"))
	if !errors.Is(err, ErrMalformedCiphertext) {
		t.Errorf("Decrypt() error = %v, want ErrMalformedCiphertext", err)
	}
}

func TestDecrypt_TamperedCiphertextFails(t *testing.T) {
	store := openTestStore(t, newMemoryBackend())
	set, err := store.LoadOrGenerate(context.Background())
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}

	ciphertext, err := set.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}
	tampered := append([]byte{}, ciphertext...)
	tampered[len(tampered)-1] ^= 0xFF

	_, err = set.Decrypt(tampered)
	if err == nil {
		t.Error("Decrypt() of tampered ciphertext succeeded, want a GCM authentication failure")
	}
}
