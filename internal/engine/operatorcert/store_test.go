package operatorcert

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func openTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	store := NewStore(conn)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return store, conn
}

func uniqueName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test-operator-%d", time.Now().UnixNano())
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, _ := openTestStore(t)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() error: %v", err)
	}
}

func TestRecordIssuance_ThenSerialForNameFindsIt(t *testing.T) {
	store, _ := openTestStore(t)
	name := uniqueName(t)

	if err := store.RecordIssuance(context.Background(), name, "11:22:33", time.Now().Add(90*24*time.Hour)); err != nil {
		t.Fatalf("RecordIssuance() error: %v", err)
	}

	got, err := store.SerialForName(context.Background(), name)
	if err != nil {
		t.Fatalf("SerialForName() error: %v", err)
	}
	if got != "11:22:33" {
		t.Errorf("SerialForName() = %q, want %q", got, "11:22:33")
	}
}

func TestSerialForName_UnknownNameReturnsErrCertificateNotFound(t *testing.T) {
	store, _ := openTestStore(t)

	_, err := store.SerialForName(context.Background(), uniqueName(t))
	if !errors.Is(err, ErrCertificateNotFound) {
		t.Errorf("SerialForName() error = %v, want ErrCertificateNotFound", err)
	}
}

func TestSerialForName_ReturnsMostRecentIssuance(t *testing.T) {
	store, _ := openTestStore(t)
	name := uniqueName(t)
	ctx := context.Background()

	if err := store.RecordIssuance(ctx, name, "old-serial", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("RecordIssuance() (old) error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.RecordIssuance(ctx, name, "new-serial", time.Now().Add(90*24*time.Hour)); err != nil {
		t.Fatalf("RecordIssuance() (new) error: %v", err)
	}

	got, err := store.SerialForName(ctx, name)
	if err != nil {
		t.Fatalf("SerialForName() error: %v", err)
	}
	if got != "new-serial" {
		t.Errorf("SerialForName() = %q, want %q (the most recent issuance)", got, "new-serial")
	}
}

func TestMarkRevoked_RemovesFromSerialForNameLookup(t *testing.T) {
	store, _ := openTestStore(t)
	name := uniqueName(t)
	ctx := context.Background()

	if err := store.RecordIssuance(ctx, name, "11:22:33", time.Now().Add(90*24*time.Hour)); err != nil {
		t.Fatalf("RecordIssuance() error: %v", err)
	}
	if err := store.MarkRevoked(ctx, name); err != nil {
		t.Fatalf("MarkRevoked() error: %v", err)
	}

	_, err := store.SerialForName(ctx, name)
	if !errors.Is(err, ErrCertificateNotFound) {
		t.Errorf("SerialForName() after revocation error = %v, want ErrCertificateNotFound", err)
	}
}

func TestMarkRevoked_RevokesAllLiveRowsForName(t *testing.T) {
	store, conn := openTestStore(t)
	name := uniqueName(t)
	ctx := context.Background()

	if err := store.RecordIssuance(ctx, name, "serial-a", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("RecordIssuance() (a) error: %v", err)
	}
	if err := store.RecordIssuance(ctx, name, "serial-b", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("RecordIssuance() (b) error: %v", err)
	}
	if err := store.MarkRevoked(ctx, name); err != nil {
		t.Fatalf("MarkRevoked() error: %v", err)
	}

	var liveCount int
	if err := conn.QueryRowContext(ctx,
		"SELECT count(*) FROM system.operator_certificates WHERE name = $1 AND revoked_at IS NULL", name,
	).Scan(&liveCount); err != nil {
		t.Fatalf("count live rows: %v", err)
	}
	if liveCount != 0 {
		t.Errorf("live rows after MarkRevoked() = %d, want 0", liveCount)
	}
}
