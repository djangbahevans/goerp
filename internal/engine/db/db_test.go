package db

import (
	"testing"
)

// localPostgresDSN points at the compose.dev.yml Postgres instance via
// PgBouncer (localhost:6432, user/pass/db "goerp"/"dev"/"goerp" — see
// README.md's local development section).
const localPostgresDSN = "postgres://goerp:dev@localhost:6432/goerp"

func TestNewConnectsAndPings(t *testing.T) {
	pool, err := New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	defer func() { _ = pool.Close() }()

	if err := pool.Ping(); err != nil {
		t.Errorf("Ping() error: %v", err)
	}
}

func TestNewMalformedDSN(t *testing.T) {
	_, err := New("not-a-valid-dsn")
	if err == nil {
		t.Fatal("New() with a malformed DSN: expected an error, got nil")
	}
}

func TestNewUnreachable(t *testing.T) {
	_, err := New("postgres://user:pass@127.0.0.1:1/db")
	if err == nil {
		t.Fatal("New() against an unreachable address: expected an error, got nil")
	}
}
