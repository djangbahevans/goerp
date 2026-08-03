package db

import (
	"context"
	"testing"
	"time"
)

// localPostgresDSN points at the compose.dev.yml Postgres instance via
// PgBouncer (localhost:6432, user/pass/db "goerp"/"dev"/"goerp" — see
// README.md's local development section).
const localPostgresDSN = "postgres://goerp:dev@localhost:6432/goerp"

func TestNewConnectsAndPings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := New(ctx, localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Errorf("Ping() error: %v", err)
	}
}

func TestNewMalformedDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := New(ctx, "not-a-valid-dsn")
	if err == nil {
		t.Fatal("New() with a malformed DSN: expected an error, got nil")
	}
}

func TestNewUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := New(ctx, "postgres://user:pass@127.0.0.1:1/db")
	if err == nil {
		t.Fatal("New() against an unreachable address: expected an error, got nil")
	}
}
