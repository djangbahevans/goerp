package signingkey

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance (bypassing PgBouncer), same convention as auditlog.Store's
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
	lockSigningKeyTable(t, conn)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.jwt_signing_keys`)
	})

	return conn
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
// schema.TestBootstrap_ConcurrentCallsAllSucceed's doc comment for what
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
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'jwt_signing_keys_active_unique_idx'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected jwt_signing_keys_active_unique_idx to exist: %v", err)
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
	if first.Active.KID == "" {
		t.Fatal("expected a non-empty KID")
	}
	if first.Active.Algorithm != "RS256" {
		t.Errorf("Algorithm = %q, want RS256", first.Active.Algorithm)
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
	if second.Active.KID != first.Active.KID {
		t.Errorf("second LoadOrGenerate() KID = %q, want the same as the first (%q) — should load, not regenerate", second.Active.KID, first.Active.KID)
	}
	if !second.Active.Private.Equal(first.Active.Private) {
		t.Error("second LoadOrGenerate() loaded a different private key than the first generated")
	}
}

func TestLoadOrGenerate_KeyRoundTripsSignVerify(t *testing.T) {
	store := openTestStore(t, newMemoryBackend())

	set, err := store.LoadOrGenerate(context.Background())
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}

	if set.Active.Private.PublicKey.Equal(set.Active.Public) == false {
		t.Error("Public does not match Private.PublicKey")
	}
	if set.Active.Private.N.BitLen() < 2040 {
		t.Errorf("key size = %d bits, want RSA-2048", set.Active.Private.N.BitLen())
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
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM system.jwt_signing_keys`).Scan(&rowCount); err != nil {
		t.Fatalf("count jwt_signing_keys rows: %v", err)
	}
	if rowCount != 0 {
		t.Errorf("jwt_signing_keys row count = %d, want 0 — an ephemeral key must not be persisted", rowCount)
	}

	second, err := store.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("second LoadOrGenerate() error: %v", err)
	}
	if second.Active.KID == first.Active.KID {
		t.Error("second LoadOrGenerate() returned the same KID as the first — an ephemeral key should regenerate every call, not persist")
	}
}
